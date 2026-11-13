// Package ipc is the daemon protocol (spec §13.2a): one JSON object per
// line over a Unix socket next to the index. The daemon is the single
// writer of the index; the app and the CLI are clients. The wire format is
// the Rust implementation's, so the SwiftUI app talks to either.
//
// Local IPC only: the socket is a file, there is no TCP listener, and the
// macOS sandbox profile permits Unix sockets in that folder alone.
package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/query"
)

// ProtocolVersion is bumped on incompatible changes; reported by status.
const ProtocolVersion = 5

// Ops.
const (
	OpPing     = "ping"
	OpStatus   = "status"
	OpSearch   = "search"
	OpIndex    = "index"
	OpForget   = "forget"
	OpRefresh  = "refresh"
	OpRoots    = "roots"
	OpStats    = "stats"
	OpShutdown = "shutdown"
	// Drives (§9.6).
	OpDrives       = "drives"
	OpDrivePlan    = "drive_plan"
	OpDriveIndex   = "drive_index"
	OpDriveAttach  = "drive_attach"
	OpDriveDetach  = "drive_detach"
	OpDriveRebuild = "drive_rebuild"
	OpDriveDelete  = "drive_delete"
	OpDriveEject   = "drive_eject"
	// Duplicates (§4).
	OpDupes       = "dupes"
	OpCleanupPlan = "cleanup_plan"
	OpCleanup     = "cleanup"
	// Content search (§2.4).
	OpContentStatus      = "content_status"
	OpSetContentSettings = "set_content_settings"
	OpUpdateContent      = "update_content"
	// Guidance (§8.3, §8.9.5).
	OpCoach = "coach"
	// OpOpened records that the user opened a result (Path), for ranking.
	OpOpened = "opened"
	// OpRecentOpens lists the results opened most recently (Limit).
	OpRecentOpens = "recent_opens"
	// OpClearOpens forgets the open history.
	OpClearOpens = "clear_opens"
	// OpExclusions lists excluded folders; OpExclude / OpInclude (Path) change them.
	OpExclusions  = "exclusions"
	OpExclude     = "exclude"
	OpInclude     = "include"
	OpValueCounts = "value_counts"
	// Typing assistance, served by the daemon because the app has no
	// in-process core (Go port; the Rust app called these through FFI).
	OpHint        = "hint"
	OpSectionAt   = "section_at"
	OpCompletions = "completions"
	OpFilters     = "filters"
	OpCheatSheet  = "cheat_sheet"
	// Tier 2 (§3.1.1): deep probing now; loudness of one file (§3.6).
	OpDeepProbe = "deep_probe"
	OpLoudness  = "loudness"
	// OpStorage lists a folder's entries by size (Path; "" = indexed folders).
	OpStorage = "storage"
	// OpSnippet: the passage of Path where the words of Query were found.
	OpSnippet = "snippet"
	// OpUnreadable: what the last full index of each folder could not read.
	OpUnreadable = "unreadable"
	// OpBackground pauses or resumes background work (Paused): the app pauses
	// it on battery, when the Mac is hot, or in Low Power Mode.
	OpBackground = "background"
)

// Request is one call. Op selects it; the other fields are per op and
// default as in the Rust protocol (limit 100, typo true).
type Request struct {
	Op string `json:"op"`
	// search
	Query   string `json:"query,omitempty"`
	Sort    string `json:"sort,omitempty"`
	Reverse bool   `json:"reverse,omitempty"`
	Offset  int    `json:"offset,omitempty"`
	Limit   *int   `json:"limit,omitempty"`
	Typo    *bool  `json:"typo,omitempty"`
	// index, forget
	Path string `json:"path,omitempty"`
	// background
	Paused *bool `json:"paused,omitempty"`
	// hint, section_at, completions: caret position (runes); lang: en/ar.
	Cursor int    `json:"cursor,omitempty"`
	Lang   string `json:"lang,omitempty"`
	// drive ops
	Mount      string  `json:"mount,omitempty"`
	Passphrase *string `json:"passphrase,omitempty"`
	// dupes: DupeOptions; cleanup_plan, cleanup: CleanupRequest;
	// set_content_settings: ContentSettings (decoded by the daemon).
	Options  json.RawMessage `json:"options,omitempty"`
	Request  json.RawMessage `json:"request,omitempty"`
	Settings json.RawMessage `json:"settings,omitempty"`
}

// LimitOr is the limit, or 100.
func (r *Request) LimitOr() int {
	if r.Limit == nil {
		return 100
	}
	return *r.Limit
}

// TypoOr is the typo flag, or true.
func (r *Request) TypoOr() bool { return r.Typo == nil || *r.Typo }

// RemoteError is a failed request. Query errors keep their position and
// suggestion so the UI can underline the exact spot (§8.1).
type RemoteError struct {
	Message    string      `json:"message"`
	Span       *query.Span `json:"span"`
	Suggestion *string     `json:"suggestion"`
	// Code is a machine-readable kind, e.g. "unsupported".
	Code string `json:"code,omitempty"`
}

func (e *RemoteError) Error() string {
	if e.Suggestion != nil {
		return e.Message + " — try `" + *e.Suggestion + "`"
	}
	return e.Message
}

// ErrorOf converts err, keeping a query error's position.
func ErrorOf(err error) *RemoteError {
	var qe *query.Error
	if errors.As(err, &qe) {
		re := &RemoteError{Message: qe.Message, Span: &query.Span{Start: qe.Span.Start, End: qe.Span.End}}
		if qe.Suggestion != "" {
			s := qe.Suggestion
			re.Suggestion = &s
		}
		return re
	}
	var re *RemoteError
	if errors.As(err, &re) {
		return re
	}
	var coded interface{ Code() string }
	if errors.As(err, &coded) {
		return &RemoteError{Message: err.Error(), Code: coded.Code()}
	}
	return &RemoteError{Message: err.Error()}
}

// Response is one reply.
type Response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RemoteError    `json:"error,omitempty"`
}

// Status is what the daemon says about itself (also the privacy audit,
// §7.6).
type Status struct {
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
	PID      int    `json:"pid"`
	// NetworkDenied: the OS refuses this process network access (§7.1).
	NetworkDenied bool `json:"network_denied"`
	// Watcher: how changes are detected (FsEvents, Inotify, Poll; "None"
	// until G3).
	Watcher string `json:"watcher"`
	DB      string `json:"db"`
	Entries uint64 `json:"entries"`
	// Indexing: a folder being indexed now (protocol 5 addition; absent
	// otherwise and from older daemons).
	Indexing *IndexProgress `json:"indexing,omitempty"`
	// CatchUp: the watcher's hourly comparison with the disk (absent from
	// older daemons and when not watching).
	CatchUp *CatchUp `json:"catch_up,omitempty"`
	// Dupes: the duplicate scan running now (see core.DupeProgress).
	Dupes *DupeProgress `json:"dupes,omitempty"`
}

// CatchUp: see core.WatcherHealth.
type CatchUp struct {
	LastCatchUp int64  `json:"last_catch_up"`
	Missed      uint64 `json:"missed"`
	Restarts    uint64 `json:"restarts"`
}

// DupeProgress: see core.DupeProgress.
type DupeProgress struct {
	Phase string `json:"phase"`
	Done  uint64 `json:"done"`
	Total uint64 `json:"total"`
}

// IndexProgress: see core.IndexProgress.
type IndexProgress struct {
	Root  string `json:"root"`
	Phase string `json:"phase"`
	Done  uint64 `json:"done"`
	Total uint64 `json:"total"`
}

// maxSocketPath: the longest Unix socket path macOS accepts (sun_path is
// 104 bytes).
const maxSocketPath = 100

// SocketFor is the socket for an index database: <db folder>/laserdotd.sock,
// or, when that is too long for a Unix socket,
// /tmp/laserdot-<uid>/<hash>.sock in an owner-only folder.
func SocketFor(db string) string {
	preferred := filepath.Join(filepath.Dir(db), "laserdotd.sock")
	if len(preferred) <= maxSocketPath {
		return preferred
	}
	// FNV-1a of the database path: stable, distinct per index.
	h := uint64(0xcbf29ce484222325)
	for _, b := range []byte(db) {
		h = (h ^ uint64(b)) * 0x100000001b3
	}
	short := fmt.Sprintf("/tmp/laserdot-%d", os.Getuid())
	os.MkdirAll(short, 0o700)
	os.Chmod(short, 0o700)
	if real, err := filepath.EvalSymlinks(short); err == nil {
		short = real
	}
	return filepath.Join(short, fmt.Sprintf("%016x.sock", h))
}

// Client is a connection to the daemon.
type Client struct {
	conn net.Conn
	r    *bufio.Reader
}

// Connect dials the daemon's socket.
func Connect(socket string) (*Client, error) {
	c, err := net.Dial("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("cannot reach laserdotd: %w", err)
	}
	return &Client{conn: c, r: bufio.NewReaderSize(c, 1<<16)}, nil
}

// ConnectLive connects only if a daemon answers on socket.
func ConnectLive(socket string) *Client {
	c, err := Connect(socket)
	if err != nil {
		return nil
	}
	var pong string
	if err := c.Call(&Request{Op: OpPing}, &pong); err != nil || pong != "pong" {
		c.Close()
		return nil
	}
	return c
}

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }

// Call sends req and decodes the result into out (which may be nil).
func (c *Client) Call(req *Request, out any) error {
	line, err := json.Marshal(req)
	if err != nil {
		return err
	}
	c.conn.SetDeadline(time.Now().Add(10 * time.Minute))
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("cannot reach laserdotd: %w", err)
	}
	reply, err := c.r.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("laserdotd protocol error: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(reply, &resp); err != nil {
		return fmt.Errorf("laserdotd protocol error: %w", err)
	}
	switch {
	case !resp.OK && resp.Error != nil:
		return resp.Error
	case !resp.OK:
		return errors.New("laserdotd protocol error: malformed response")
	case out == nil:
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}
