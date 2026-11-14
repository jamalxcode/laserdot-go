// Package daemon serves the index over the IPC socket (spec §13.2a). It is
// the single writer of the index; the app and the CLI are clients.
package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/core"
	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/ipc"
	"github.com/jamalxcode/laserdot-go/internal/query"
	"github.com/jamalxcode/laserdot-go/internal/sandbox"
)

// Version of laserdot.
// Version is stamped at build time (app/scripts/build-go.sh, -X) with the
// app's version; the app replaces a running daemon that reports another.
var Version = "dev"

// maxLine bounds one request. Most are short, but a duplicate cleanup lists
// every file to move and every copy kept (tens of thousands of paths): the
// app sends those in batches, and this leaves room for a big batch while
// still stopping a runaway client.
const maxLine = 64 << 20

// Server answers requests for one index.
type Server struct {
	ld *core.Laserdot
	// contentDirty: files may have changed; the content index needs a look.
	contentDirty atomic.Bool
	// paused: background work (contents, Tier 2) waits — on battery, hot, or
	// in Low Power Mode, as the app reports.
	paused    atomic.Bool
	contentMu sync.Mutex // one content update at a time
	// watcher keeps the index current (nil when not watching).
	watcher *core.Watcher
	socket  string
	ln      net.Listener
	once    sync.Once
	done    chan struct{}
	conns   sync.WaitGroup
}

// Listen opens the socket (owner-only) for ld. An existing live daemon on
// the socket is an error; a stale socket file is replaced.
func Listen(ld *core.Laserdot, socket string) (*Server, error) {
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return nil, err
	}
	if c := ipc.ConnectLive(socket); c != nil {
		c.Close()
		return nil, fmt.Errorf("already running on %s", socket)
	}
	os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return &Server{ld: ld, socket: socket, ln: ln, done: make(chan struct{})}, nil
}

// SetWatcher attaches the watcher (status reports its backend, roots
// changes restart it).
func (s *Server) SetWatcher(w *core.Watcher) { s.watcher = w }

func (s *Server) rootsChanged() {
	if s.watcher != nil {
		s.watcher.RootsChanged()
	}
	s.contentDirty.Store(true)
}

// MarkContentDirty asks the content loop to look for changed documents.
func (s *Server) MarkContentDirty() { s.contentDirty.Store(true) }

// UpdateContent brings the content index up to date (one at a time).
func (s *Server) UpdateContent() (core.ContentStats, error) {
	s.contentMu.Lock()
	defer s.contentMu.Unlock()
	return s.ld.UpdateContent()
}

// RunBackground starts the content and mount loops until Stop. log may be
// nil.
func (s *Server) RunBackground(volumeEvery time.Duration, log func(string)) {
	if log == nil {
		log = func(string) {}
	}
	s.contentDirty.Store(true)
	go func() {
		for {
			select {
			case <-s.done:
				return
			case <-time.After(2 * time.Second):
			}
			if s.paused.Load() || !s.contentDirty.Swap(false) {
				continue
			}
			st, err := s.UpdateContent()
			if err != nil {
				log("content update failed: " + err.Error())
			} else if st.Indexed+st.Skipped+st.Removed > 0 {
				log(fmt.Sprintf("content: %d indexed, %d skipped, %d removed in %d ms", st.Indexed, st.Skipped, st.Removed, st.MS))
			}
		}
	}()
	// Tier 2 at idle: a few files at a time, never on the indexing path.
	go func() {
		for {
			select {
			case <-s.done:
				return
			case <-time.After(2 * time.Minute):
			}
			if s.paused.Load() {
				continue
			}
			st, err := s.ld.DeepProbe(20)
			if err != nil {
				log("deep probe failed: " + err.Error())
			} else if st.Updated > 0 {
				log(fmt.Sprintf("deep probe: %d files updated (%d left)", st.Updated, st.Remaining))
			}
		}
	}()
	if volumeEvery <= 0 {
		return
	}
	go func() {
		for {
			attached, gone := s.ld.SyncDrives()
			for _, m := range attached {
				log("attached drive " + m)
			}
			for _, m := range gone {
				log("detached drive " + m)
			}
			if len(gone) > 0 {
				// A drive left: its index kept on this Mac (read-only
				// drives) goes too.
				for _, p := range s.ld.PurgeOffDriveIndexes() {
					log("deleted the index of a drive that is gone: " + p)
				}
			}
			if len(attached)+len(gone) > 0 {
				s.rootsChanged()
			}
			select {
			case <-s.done:
				return
			case <-time.After(volumeEvery):
			}
		}
	}()
}

// Paused: background work waits (battery, heat, Low Power Mode).
func (s *Server) Paused() bool { return s.paused.Load() }

func (s *Server) catchUp() *ipc.CatchUp {
	if s.watcher == nil {
		return nil
	}
	h := s.watcher.Health()
	return &ipc.CatchUp{LastCatchUp: h.LastCatchUp, Missed: h.Missed, Restarts: h.Restarts}
}

func (s *Server) watcherName() string {
	if s.watcher == nil {
		return "None"
	}
	return s.watcher.Backend()
}

// Serve accepts connections until Stop (or a shutdown request).
func (s *Server) Serve() error {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.done:
				s.conns.Wait()
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		s.conns.Add(1)
		go func() {
			defer s.conns.Done()
			s.serveConn(c)
		}()
	}
}

// Stop closes the socket; Serve returns once open requests finish.
func (s *Server) Stop() {
	s.once.Do(func() {
		close(s.done)
		s.ln.Close()
		os.Remove(s.socket)
	})
}

// Done is closed when the server stops.
func (s *Server) Done() <-chan struct{} { return s.done }

func (s *Server) serveConn(c net.Conn) {
	defer c.Close()
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	w := bufio.NewWriter(c)
	go func() { <-s.done; c.Close() }()
	for sc.Scan() {
		var req ipc.Request
		var resp ipc.Response
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			resp = fail(&ipc.RemoteError{Message: "bad request: " + err.Error(), Code: "bad_request"})
		} else {
			resp = s.Handle(&req)
		}
		line, _ := json.Marshal(resp)
		w.Write(line)
		w.WriteByte('\n')
		if w.Flush() != nil {
			return
		}
		if req.Op == ipc.OpShutdown && resp.OK {
			s.Stop()
			return
		}
	}
}

func ok(v any) ipc.Response {
	b, err := json.Marshal(v)
	if err != nil {
		return fail(ipc.ErrorOf(err))
	}
	return ipc.Response{OK: true, Result: b}
}

func fail(e *ipc.RemoteError) ipc.Response { return ipc.Response{Error: e} }

func badRequest(err error) ipc.Response {
	return fail(&ipc.RemoteError{Message: "bad request: " + err.Error(), Code: "bad_request"})
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func result(v any, err error) ipc.Response {
	if err != nil {
		return fail(ipc.ErrorOf(err))
	}
	return ok(v)
}

// Handle answers one request.
func (s *Server) Handle(req *ipc.Request) ipc.Response {
	switch req.Op {
	case ipc.OpPing:
		return ok("pong")
	case ipc.OpStatus:
		st := ipc.Status{
			Protocol: ipc.ProtocolVersion, Version: Version, PID: os.Getpid(),
			NetworkDenied: sandbox.IsEnforced(), Watcher: s.watcherName(), CatchUp: s.catchUp(),
			DB: s.ld.DBPath(), Entries: s.ld.Stats().Entries,
		}
		if p := s.ld.Progress(); p != nil {
			st.Indexing = &ipc.IndexProgress{Root: p.Root, Phase: p.Phase, Done: p.Done, Total: p.Total}
		}
		if p := s.ld.DupeProgress(); p != nil {
			st.Dupes = &ipc.DupeProgress{Phase: p.Phase, Done: p.Done, Total: p.Total}
		}
		return ok(st)
	case ipc.OpSearch:
		srt, valid := index.ParseSort(req.Sort)
		if !valid {
			return fail(&ipc.RemoteError{Message: fmt.Sprintf("unknown sort `%s`", req.Sort), Code: "bad_request"})
		}
		opts := core.SearchOptions{
			Page:         index.Page{Sort: srt, Reverse: req.Reverse, Offset: max(req.Offset, 0), Limit: max(req.LimitOr(), 0)},
			TypoFallback: req.TypoOr(),
		}
		return result(s.ld.Search(req.Query, opts))
	case ipc.OpIndex:
		defer s.rootsChanged()
		// A full index builds the index in memory; serve it from the
		// snapshot instead (file-backed pages, as after a restart) and give
		// the build's memory back to the system.
		defer func() {
			go func() {
				_ = s.ld.Remap()
				debug.FreeOSMemory()
			}()
		}()
		return result(s.ld.Index(req.Path))
	case ipc.OpForget:
		defer s.rootsChanged()
		return result(s.ld.Forget(req.Path))
	case ipc.OpRefresh:
		defer s.MarkContentDirty()
		return result(s.ld.Refresh())
	case ipc.OpRoots:
		return result(s.ld.Roots())
	case ipc.OpStats:
		return ok(s.ld.Stats())
	case ipc.OpShutdown:
		return ok("bye")
	case ipc.OpDrives:
		return ok(s.ld.Drives())
	case ipc.OpDrivePlan:
		return result(s.ld.PlanDrive(req.Mount))
	case ipc.OpDriveIndex:
		defer s.rootsChanged()
		return result(s.ld.CreateDriveIndex(req.Mount, core.DriveOptions{Passphrase: deref(req.Passphrase)}))
	case ipc.OpDriveAttach:
		defer s.rootsChanged()
		return result(s.ld.AttachDrive(req.Mount, deref(req.Passphrase)))
	case ipc.OpDriveDetach:
		defer s.rootsChanged()
		return ok(s.ld.DetachDrive(req.Mount))
	case ipc.OpDriveRebuild:
		defer s.rootsChanged()
		return result(s.ld.RebuildDrive(req.Mount, core.DriveOptions{Passphrase: deref(req.Passphrase)}))
	case ipc.OpDriveDelete:
		defer s.rootsChanged()
		return result(s.ld.DeleteDriveIndex(req.Mount))
	case ipc.OpDriveEject:
		defer s.rootsChanged()
		if err := s.ld.EjectDrive(req.Mount); err != nil {
			return fail(ipc.ErrorOf(err))
		}
		return ok("ejected")
	case ipc.OpDupesStop:
		return ok(s.ld.StopDuplicates())
	case ipc.OpDupes:
		opts := core.DefaultDupeOptions()
		if len(req.Options) > 0 {
			if err := json.Unmarshal(req.Options, &opts); err != nil {
				return badRequest(err)
			}
		}
		return result(s.ld.FindDuplicates(opts))
	case ipc.OpCleanupPlan, ipc.OpCleanup:
		var cr core.CleanupRequest
		if err := json.Unmarshal(req.Request, &cr); err != nil {
			return badRequest(err)
		}
		if req.Op == ipc.OpCleanupPlan {
			return ok(s.ld.PlanCleanup(cr))
		}
		defer s.MarkContentDirty()
		return result(s.ld.ApplyCleanup(cr))
	case ipc.OpContentStatus:
		return result(s.ld.ContentStatus())
	case ipc.OpSetContentSettings:
		var cs core.ContentSettings
		if err := json.Unmarshal(req.Settings, &cs); err != nil {
			return badRequest(err)
		}
		if err := s.ld.SetContentSettings(cs); err != nil {
			return fail(ipc.ErrorOf(err))
		}
		s.MarkContentDirty()
		return result(s.ld.ContentStatus())
	case ipc.OpUpdateContent:
		return result(s.UpdateContent())
	case ipc.OpCoach:
		return ok(s.ld.Coach(req.Query))
	case ipc.OpUnreadable:
		return result(s.ld.Unreadable())
	case ipc.OpSnippet:
		return ok(s.ld.Snippet(req.Path, strings.Fields(req.Query)))
	case ipc.OpStorage:
		limit := 50
		if req.Limit != nil {
			limit = *req.Limit
		}
		rep, err := s.ld.Storage(req.Path, limit)
		if err != nil {
			return fail(ipc.ErrorOf(err))
		}
		return ok(rep)
	case ipc.OpBackground:
		if req.Paused != nil {
			s.paused.Store(*req.Paused)
			if !*req.Paused {
				s.contentDirty.Store(true) // catch up what waited
			}
		}
		return ok(map[string]bool{"paused": s.paused.Load()})
	case ipc.OpClearOpens:
		if err := s.ld.ClearOpens(); err != nil {
			return fail(ipc.ErrorOf(err))
		}
		return ok("ok")
	case ipc.OpExclusions:
		return ok(s.ld.Exclusions())
	case ipc.OpExclude, ipc.OpInclude:
		f := s.ld.Exclude
		if req.Op == ipc.OpInclude {
			f = s.ld.Include
		}
		list, err := f(req.Path)
		if err != nil {
			return fail(ipc.ErrorOf(err))
		}
		s.rootsChanged()
		s.MarkContentDirty()
		return ok(list)
	case ipc.OpRecentOpens:
		limit := 10
		if req.Limit != nil && *req.Limit > 0 {
			limit = *req.Limit
		}
		return ok(s.ld.RecentOpens(limit))
	case ipc.OpOpened:
		if err := s.ld.RecordOpen(req.Path); err != nil {
			return fail(ipc.ErrorOf(err))
		}
		return ok("ok")
	case ipc.OpValueCounts:
		return ok(s.ld.ValueCounts())
	case ipc.OpHint:
		if h := query.Hint(req.Query, req.Cursor, query.LangFromCode(req.Lang)); h != "" {
			return ok(h)
		}
		return ok(nil)
	case ipc.OpSectionAt:
		if sec, found := query.SectionAt(req.Query, req.Cursor); found {
			return ok(sec.Title())
		}
		return ok(nil)
	case ipc.OpCompletions:
		return ok(query.Completions(req.Query, req.Cursor, query.LangFromCode(req.Lang)))
	case ipc.OpFilters:
		return ok(query.FilterList(query.LangFromCode(req.Lang)))
	case ipc.OpDeepProbe:
		limit := 0
		if req.Limit != nil {
			limit = *req.Limit
		}
		return result(s.ld.DeepProbe(limit))
	case ipc.OpLoudness:
		return result(s.ld.Loudness(req.Path))
	case ipc.OpCheatSheet:
		return ok(query.CheatSheetIn(query.LangFromCode(req.Lang)))
	}
	return fail(&ipc.RemoteError{
		Message: fmt.Sprintf("`%s` is not supported by this laserdotd yet", req.Op),
		Code:    "unsupported",
	})
}
