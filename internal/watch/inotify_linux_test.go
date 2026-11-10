package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Port of the Rust inotify test: new files and new folders are reported
// as changes of exactly their folder.
func TestInotifyReportsNewFilesAndNewFolders(t *testing.T) {
	root := canonical(t, t.TempDir())
	if err := os.Mkdir(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := newWatcher(t, DefaultOptions(), WatchRoot{Path: root})
	if w.Backend() != Inotify || w.Replays() {
		t.Fatalf("backend %s", w.Backend())
	}
	mustWrite(t, filepath.Join(root, "a/x.txt"), "x")
	b, ok := w.NextBatch(3 * time.Second)
	if !ok || len(b.Changes) != 1 || b.Changes[0].Path != filepath.Join(root, "a") || b.Changes[0].Recursive {
		t.Fatalf("%+v", b)
	}
	if err := os.Mkdir(filepath.Join(root, "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.NextBatch(3 * time.Second); !ok {
		t.Fatal("mkdir not reported")
	}
	mustWrite(t, filepath.Join(root, "new/y.txt"), "y")
	if !waitFor(t, w, 5*time.Second, func(b Batch) bool { return hasChange(b, filepath.Join(root, "new")) }) {
		t.Fatal("file in new folder not reported")
	}
}

// A folder moved out of the tree is no longer watched.
func TestInotifyForgetsFoldersMovedOut(t *testing.T) {
	base := canonical(t, t.TempDir())
	root, outside := filepath.Join(base, "root"), filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(root, "d"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w := newWatcher(t, Options{Latency: 50 * time.Millisecond}, WatchRoot{Path: root})
	if err := os.Rename(filepath.Join(root, "d"), filepath.Join(outside, "d")); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, w, 3*time.Second, func(b Batch) bool { return hasChange(b, root) }) {
		t.Fatal("move out not reported")
	}
	mustWrite(t, filepath.Join(outside, "d/x.txt"), "x")
	if b, ok := w.NextBatch(300 * time.Millisecond); ok {
		t.Errorf("change outside the watched tree: %+v", b)
	}
}
