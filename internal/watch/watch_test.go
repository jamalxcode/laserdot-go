package watch

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/scan"
)

func canonical(t *testing.T, dir string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(dir) // /var → /private/var on macOS
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func newWatcher(t *testing.T, opts Options, roots ...WatchRoot) *Watcher {
	t.Helper()
	w, err := New(roots, opts, scan.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

// waitFor reads batches until one satisfies ok, for up to d.
func waitFor(t *testing.T, w *Watcher, d time.Duration, ok func(Batch) bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if b, got := w.NextBatch(time.Until(deadline)); got && ok(b) {
			return true
		}
	}
	return false
}

func hasChange(b Batch, path string) bool {
	for _, c := range b.Changes {
		if c.Path == path || (c.Recursive && isUnder(path, c.Path)) {
			return true
		}
	}
	return false
}

func mustWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBackendNames(t *testing.T) {
	for b, want := range map[Backend]string{FSEvents: "FsEvents", Inotify: "Inotify", Poll: "Poll"} {
		if b.String() != want {
			t.Errorf("%d: %s", b, b)
		}
	}
}

// Events within the coalescing window form one batch: changes to the same
// folder merge, a recursive change swallows those beneath it, the highest
// event id wins.
func TestBatchesCoalesce(t *testing.T) {
	w := &Watcher{q: newQueue([]string{"/r"}), latency: 500 * time.Millisecond, done: make(chan struct{})}
	w.q.changed("/r/a/b", false)
	w.q.push(event{kind: evID, id: 9})
	w.q.changed("/r/a", true)
	w.q.changed("/r/ab", false)
	w.q.changed("/r/a/b", false)
	w.q.changed("/r/c", false)
	w.q.push(event{kind: evID, id: 5})
	w.q.changed("/r/c", true)
	go func() {
		time.Sleep(30 * time.Millisecond)
		w.q.push(event{kind: evHistoryDone})
	}()
	b, ok := w.NextBatch(time.Second)
	want := []scan.Change{{Path: "/r/a", Recursive: true}, {Path: "/r/ab"}, {Path: "/r/c", Recursive: true}}
	if !ok || !reflect.DeepEqual(b.Changes, want) || b.EventID != 9 || !b.HistoryDone {
		t.Fatalf("%+v", b)
	}
	if _, ok := w.NextBatch(50 * time.Millisecond); ok {
		t.Error("batch out of nothing")
	}
}

func TestQueueOverflowBecomesRescan(t *testing.T) {
	q := newQueue([]string{"/r"})
	for i := 0; i <= maxQueued; i++ {
		q.changed("/r/x", false)
	}
	e, _ := q.pop(time.Now(), nil)
	if e.change != (scan.Change{Path: "/r", Recursive: true}) {
		t.Fatalf("%+v", e)
	}
}

func TestPollingDeliversChanges(t *testing.T) {
	root := canonical(t, t.TempDir())
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := newWatcher(t, Options{Latency: 20 * time.Millisecond, PollInterval: 50 * time.Millisecond, ForcePoll: true},
		WatchRoot{Path: root})
	if w.Backend() != Poll || w.Replays() {
		t.Fatalf("backend %s replays %v", w.Backend(), w.Replays())
	}
	// The first poll reports the root recursively (as the Rust build did on
	// every poll).
	b, ok := w.NextBatch(2 * time.Second)
	if !ok || !reflect.DeepEqual(b.Changes, []scan.Change{{Path: root, Recursive: true}}) {
		t.Fatalf("%+v", b)
	}
	// Then only folders whose listing changed.
	mustWrite(t, filepath.Join(root, "sub/x.txt"), "x")
	if !waitFor(t, w, 3*time.Second, func(b Batch) bool {
		return reflect.DeepEqual(b.Changes, []scan.Change{{Path: filepath.Join(root, "sub")}})
	}) {
		t.Fatal("new file not reported")
	}
	mustWrite(t, filepath.Join(root, "sub/x.txt"), "longer")
	if !waitFor(t, w, 3*time.Second, func(b Batch) bool { return hasChange(b, filepath.Join(root, "sub")) }) {
		t.Fatal("modified file not reported")
	}
	if err := os.Rename(filepath.Join(root, "sub/x.txt"), filepath.Join(root, "y.txt")); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, w, 3*time.Second, func(b Batch) bool {
		return hasChange(b, root) && hasChange(b, filepath.Join(root, "sub"))
	}) {
		t.Fatal("move not reported on both folders")
	}
	// Nothing changes: nothing is reported.
	if b, ok := w.NextBatch(300 * time.Millisecond); ok {
		t.Errorf("spurious %+v", b)
	}
}

func TestCloseStopsDelivery(t *testing.T) {
	root := canonical(t, t.TempDir())
	w := newWatcher(t, Options{Latency: 10 * time.Millisecond, PollInterval: 20 * time.Millisecond, ForcePoll: true},
		WatchRoot{Path: root})
	w.Close()
	w.Close()
	if _, ok := w.NextBatch(100 * time.Millisecond); ok {
		t.Error("batch after Close")
	}
}

func TestCurrentEventID(t *testing.T) {
	id, ok := CurrentEventID()
	if ok != (runtime.GOOS == "darwin") || (ok && id == 0) {
		t.Errorf("%d %v", id, ok)
	}
}

// The native backend (inotify on Linux, FSEvents on macOS) reports creates,
// modifications, renames and deletes as changes of the containing folder,
// including in folders created after the watch started.
func TestNativeReportsFileLifecycle(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no native backend")
	}
	root := canonical(t, t.TempDir())
	a := filepath.Join(root, "a")
	if err := os.Mkdir(a, 0o755); err != nil {
		t.Fatal(err)
	}
	w := newWatcher(t, Options{Latency: 50 * time.Millisecond, PollInterval: time.Hour}, WatchRoot{Path: root})
	want := map[string]Backend{"linux": Inotify, "darwin": FSEvents}[runtime.GOOS]
	if w.Backend() != want {
		t.Fatalf("backend %s", w.Backend())
	}
	if runtime.GOOS == "darwin" {
		time.Sleep(300 * time.Millisecond) // let the stream start
	}
	step := func(what, dir string, do func()) {
		t.Helper()
		for { // drain what earlier steps left behind
			if _, ok := w.NextBatch(150 * time.Millisecond); !ok {
				break
			}
		}
		do()
		if !waitFor(t, w, 5*time.Second, func(b Batch) bool { return hasChange(b, dir) }) {
			t.Fatalf("%s: no change for %s", what, dir)
		}
	}
	x, y := filepath.Join(a, "x.txt"), filepath.Join(a, "y.txt")
	step("create", a, func() { mustWrite(t, x, "x") })
	step("modify", a, func() { mustWrite(t, x, "xx") })
	step("rename", a, func() {
		if err := os.Rename(x, y); err != nil {
			t.Fatal(err)
		}
	})
	step("delete", a, func() {
		if err := os.Remove(y); err != nil {
			t.Fatal(err)
		}
	})
	nu := filepath.Join(root, "new")
	step("mkdir", root, func() {
		if err := os.Mkdir(nu, 0o755); err != nil {
			t.Fatal(err)
		}
	})
	step("create in new folder", nu, func() { mustWrite(t, filepath.Join(nu, "z.txt"), "z") })
	// A renamed folder is still watched, under its new name.
	moved := filepath.Join(root, "moved")
	step("rename folder", root, func() {
		if err := os.Rename(nu, moved); err != nil {
			t.Fatal(err)
		}
	})
	step("create in renamed folder", moved, func() { mustWrite(t, filepath.Join(moved, "w.txt"), "w") })
}

// Ignored folders (node_modules…) are not watched.
func TestNativeSkipsIgnoredFolders(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("FSEvents watches whole trees; ignore rules apply in the update engine")
	}
	root := canonical(t, t.TempDir())
	nm := filepath.Join(root, "node_modules")
	if err := os.Mkdir(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	w := newWatcher(t, Options{Latency: 50 * time.Millisecond}, WatchRoot{Path: root})
	mustWrite(t, filepath.Join(nm, "x.js"), "x")
	if b, ok := w.NextBatch(300 * time.Millisecond); ok {
		t.Errorf("change inside an ignored folder: %+v", b)
	}
}
