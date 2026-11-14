package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/core"
	"github.com/jamalxcode/laserdot-go/internal/ipc"
)

// shortDir: socket paths are limited to ~104 bytes on macOS.
func shortDir(t *testing.T) string {
	d, err := os.MkdirTemp("/tmp", "ldd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	d, _ = filepath.EvalSymlinks(d)
	return d
}

func start(t *testing.T) (dir string, c *ipc.Client, srv *Server) {
	dir = shortDir(t)
	os.MkdirAll(filepath.Join(dir, "files", "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, "files", "docs", "invoice march.pdf"), []byte("x"), 0o644)
	ld, err := core.Open(filepath.Join(dir, "db", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ld.Close() })
	srv, err = Listen(ld, ipc.SocketFor(ld.DBPath()))
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	t.Cleanup(srv.Stop)
	c, err = ipc.Connect(ipc.SocketFor(ld.DBPath()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return dir, c, srv
}

func TestProtocol(t *testing.T) {
	dir, c, _ := start(t)
	var pong string
	if err := c.Call(&ipc.Request{Op: ipc.OpPing}, &pong); err != nil || pong != "pong" {
		t.Fatal(pong, err)
	}
	var st core.IndexStats
	if err := c.Call(&ipc.Request{Op: ipc.OpIndex, Path: filepath.Join(dir, "files")}, &st); err != nil || st.Files != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	var res core.SearchResults
	if err := c.Call(&ipc.Request{Op: ipc.OpSearch, Query: "march invoice"}, &res); err != nil || res.Total != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if res.Hits[0].Name != "invoice march.pdf" || res.Hits[0].Kind != "document" {
		t.Errorf("%+v", res.Hits[0])
	}
	// Query errors keep their position and suggestion.
	err := c.Call(&ipc.Request{Op: ipc.OpSearch, Query: "res:4l"}, &res)
	re, ok := err.(*ipc.RemoteError)
	if !ok || re.Span == nil || re.Span.Start != 4 || re.Suggestion == nil || *re.Suggestion != "res:4k" {
		t.Errorf("%#v", err)
	}
	var roots []map[string]any
	if err := c.Call(&ipc.Request{Op: ipc.OpRoots}, &roots); err != nil || len(roots) != 1 || roots[0]["complete"] != true {
		t.Errorf("%v %v", roots, err)
	}
	var status ipc.Status
	if err := c.Call(&ipc.Request{Op: ipc.OpStatus}, &status); err != nil || status.Protocol != ipc.ProtocolVersion || status.Entries != 2 {
		t.Errorf("%+v %v", status, err)
	}
	err = c.Call(&ipc.Request{Op: "frobnicate"}, nil)
	if re, ok := err.(*ipc.RemoteError); !ok || re.Code != "unsupported" {
		t.Errorf("%v", err)
	}
	// The G4 ops, with the Rust wire shapes.
	var coach []map[string]any
	if err := c.Call(&ipc.Request{Op: ipc.OpCoach, Query: "invoice res:8k"}, &coach); err != nil || coach[0]["kind"] != "relax" || coach[0]["query"] != "invoice" {
		t.Errorf("%v %v", coach, err)
	}
	var counts []map[string]any
	if err := c.Call(&ipc.Request{Op: ipc.OpValueCounts}, &counts); err != nil || len(counts) < 50 {
		t.Errorf("%d %v", len(counts), err)
	}
	var drives []map[string]any
	if err := c.Call(&ipc.Request{Op: ipc.OpDrives}, &drives); err != nil {
		t.Error(err)
	}
	var dupes map[string]any
	if err := c.Call(&ipc.Request{Op: ipc.OpDupes, Options: json.RawMessage(`{"images":false}`)}, &dupes); err != nil || dupes["files"] != 1.0 {
		t.Errorf("%v %v", dupes, err)
	}
	var cst map[string]any
	if err := c.Call(&ipc.Request{Op: ipc.OpSetContentSettings, Settings: json.RawMessage(`{"roots":["` + filepath.Join(dir, "files") + `"],"max_bytes":1000}`)}, &cst); err != nil || cst["settings"] == nil {
		t.Errorf("%v %v", cst, err)
	}
	var plan map[string]any
	if err := c.Call(&ipc.Request{Op: ipc.OpCleanupPlan, Request: json.RawMessage(`{"groups":[],"dest":{"to":"trash"}}`)}, &plan); err != nil || plan["bytes"] != 0.0 {
		t.Errorf("%v %v", plan, err)
	}
	var hint string
	if err := c.Call(&ipc.Request{Op: ipc.OpHint, Query: "res:", Cursor: 4, Lang: "ar"}, &hint); err != nil || !strings.Contains(hint, "الدقة") {
		t.Errorf("%q %v", hint, err)
	}
	var comps []map[string]any
	if err := c.Call(&ipc.Request{Op: ipc.OpCompletions, Query: "res:1", Cursor: 5}, &comps); err != nil || len(comps) != 2 {
		t.Errorf("%v %v", comps, err)
	}
	var section *string
	if err := c.Call(&ipc.Request{Op: ipc.OpSectionAt, Query: "chan", Cursor: 4}, &section); err != nil || section == nil || *section != "Audio" {
		t.Errorf("%v %v", section, err)
	}
	err = c.Call(&ipc.Request{Op: ipc.OpDriveAttach, Mount: dir}, nil)
	if _, ok := err.(*ipc.RemoteError); !ok {
		t.Errorf("attaching a folder with no index: %v", err)
	}
}

// The wire format is the Rust daemon's: snake_case fields, an "op" tag,
// defaults for omitted fields.
func TestWireFormat(t *testing.T) {
	dir, _, srv := start(t)
	conn, err := net.Dial("unix", ipc.SocketFor(filepath.Join(dir, "db", "index.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte(`{"op":"search","query":"zzz"}` + "\n" + `not json` + "\n"))
	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var got string
	for strings.Count(got, "\n") < 2 {
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatal(err, got)
		}
		got += string(buf[:n])
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	var first map[string]any
	json.Unmarshal([]byte(lines[0]), &first)
	r, _ := first["result"].(map[string]any)
	for _, k := range []string{"total", "hits", "typo_fallback", "warnings", "elapsed_us"} {
		if _, ok := r[k]; !ok {
			t.Errorf("result lacks %s: %s", k, lines[0])
		}
	}
	if !strings.Contains(lines[1], `"ok":false`) || !strings.Contains(lines[1], "bad request") {
		t.Errorf("%s", lines[1])
	}
	_ = srv
}

func TestShutdownStopsTheServer(t *testing.T) {
	_, c, srv := start(t)
	var bye string
	if err := c.Call(&ipc.Request{Op: ipc.OpShutdown}, &bye); err != nil || bye != "bye" {
		t.Fatal(bye, err)
	}
	select {
	case <-srv.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("still running")
	}
}

// Privacy gate (§15.5): the real daemon reports that the OS denies it the
// network.
func TestDaemonBinaryIsSandboxed(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no sandbox on this OS")
	}
	dir := shortDir(t)
	bin := filepath.Join(dir, "laserdotd")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/laserdotd").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	db := filepath.Join(dir, "db", "index.db")
	cmd := exec.Command(bin, "--db", db)
	var logs strings.Builder
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	var c *ipc.Client
	for i := 0; i < 100 && c == nil; i++ {
		time.Sleep(100 * time.Millisecond)
		c = ipc.ConnectLive(ipc.SocketFor(db))
	}
	if c == nil {
		t.Fatalf("daemon did not start:\n%s", logs.String())
	}
	defer c.Close()
	var st ipc.Status
	if err := c.Call(&ipc.Request{Op: ipc.OpStatus}, &st); err != nil {
		t.Fatal(err)
	}
	if !st.NetworkDenied || st.PID != cmd.Process.Pid {
		t.Errorf("%+v\n%s", st, logs.String())
	}
	c.Call(&ipc.Request{Op: ipc.OpShutdown}, nil)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("exit: %v\n%s", err, logs.String())
		}
	case <-time.After(10 * time.Second):
		t.Error("daemon did not exit after shutdown")
	}
}

// A duplicate cleanup lists thousands of files in one request (several MB):
// the daemon answers it instead of hanging up ("Broken pipe").
func TestBigRequestsAreAnswered(t *testing.T) {
	dir, _, _ := start(t)
	conn, err := net.Dial("unix", ipc.SocketFor(filepath.Join(dir, "db", "index.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var groups []string
	for i := 0; i < 30000; i++ {
		p := fmt.Sprintf(`/nowhere/some/long/folder/name/copy-%06d.mov`, i)
		groups = append(groups, fmt.Sprintf(`{"keep":[{"path":%q,"size":1,"modified":1}],"remove":[{"path":%q,"size":1,"modified":1}]}`, p+".a", p+".b"))
	}
	req := `{"op":"cleanup_plan","request":{"groups":[` + strings.Join(groups, ",") + `],"dest":{"to":"trash"}}}` + "\n"
	if len(req) < 2<<20 {
		t.Fatalf("request only %d bytes", len(req))
	}
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("no answer to a %d-byte request: %v", len(req), err)
	}
	if !bytes.Contains(line, []byte(`"ok"`)) && !bytes.Contains(line, []byte(`"skipped"`)) {
		t.Fatalf("unexpected answer: %.300s", line)
	}
}

// A bug in one request is answered as an error and recorded; the daemon goes
// on answering.
func TestABugInARequestDoesNotStopTheDaemon(t *testing.T) {
	_, c, _ := start(t)
	panicOnOp = ipc.OpStats
	defer func() { panicOnOp = "" }()
	var v any
	err := c.Call(&ipc.Request{Op: ipc.OpStats}, &v)
	if err == nil || !strings.Contains(err.Error(), "kept running") {
		t.Fatalf("a bug was not answered as an error: %v", err)
	}
	var pong string
	if err := c.Call(&ipc.Request{Op: ipc.OpPing}, &pong); err != nil || pong != "pong" {
		t.Fatalf("the daemon stopped answering after a bug: %q %v", pong, err)
	}
	var st map[string]any
	if err := c.Call(&ipc.Request{Op: ipc.OpStatus}, &st); err != nil || st["bugs_caught"] == nil {
		t.Fatalf("the bug was not counted: %v %v", st["bugs_caught"], err)
	}
}
