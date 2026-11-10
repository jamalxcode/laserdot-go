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
	"sync"

	"github.com/jamalxcode/laserdot-go/internal/core"
	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/ipc"
	"github.com/jamalxcode/laserdot-go/internal/sandbox"
)

// Version of laserdot.
const Version = "0.1.0-go"

// maxLine bounds one request (a query is short; this stops a runaway
// client from growing memory without bound).
const maxLine = 1 << 20

// Server answers requests for one index.
type Server struct {
	ld *core.Laserdot
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
		return ok(ipc.Status{
			Protocol: ipc.ProtocolVersion, Version: Version, PID: os.Getpid(),
			NetworkDenied: sandbox.IsEnforced(), Watcher: s.watcherName(),
			DB: s.ld.DBPath(), Entries: s.ld.Stats().Entries,
		})
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
		return result(s.ld.Index(req.Path))
	case ipc.OpForget:
		defer s.rootsChanged()
		return result(s.ld.Forget(req.Path))
	case ipc.OpRefresh:
		return result(s.ld.Refresh())
	case ipc.OpRoots:
		return result(s.ld.Roots())
	case ipc.OpStats:
		return ok(s.ld.Stats())
	case ipc.OpShutdown:
		return ok("bye")
	}
	return fail(&ipc.RemoteError{
		Message: fmt.Sprintf("`%s` is not supported by this laserdotd yet", req.Op),
		Code:    "unsupported",
	})
}
