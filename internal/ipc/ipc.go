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
