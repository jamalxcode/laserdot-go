package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Port of the Rust test: live FSEvents delivery, with event ids.
func TestFSEventsReportsChanges(t *testing.T) {
	root := canonical(t, t.TempDir())
	w := newWatcher(t, DefaultOptions(), WatchRoot{Path: root})
	if w.Backend() != FSEvents {
		t.Fatalf("backend %s", w.Backend())
	}
	time.Sleep(200 * time.Millisecond)
	mustWrite(t, filepath.Join(root, "x.txt"), "x")
	seen := waitFor(t, w, 20*time.Second, func(b Batch) bool {
		if b.EventID == 0 {
			t.Errorf("no event id: %+v", b)
		}
		for _, c := range b.Changes {
			if isUnder(c.Path, root) {
				return true
			}
		}
		return false
	})
	if !seen {
		t.Fatalf("no FSEvents change for %s", root)
	}
}

// Port of the Rust test: changes made while not watching are replayed from a
// stored event id, followed by HistoryDone.
func TestFSEventsReplaysHistorySinceEventID(t *testing.T) {
	root := canonical(t, t.TempDir())
	since, ok := CurrentEventID()
	if !ok {
		t.Fatal("no event id")
	}
	mustWrite(t, filepath.Join(root, "while-closed.txt"), "x")
	time.Sleep(2 * time.Second) // let fseventsd record it
	w := newWatcher(t, DefaultOptions(), WatchRoot{Path: root, Since: since})
	if !w.Replays() {
		t.Fatal("Replays() is false with a stored id")
	}
	seen, done := false, false
	waitFor(t, w, 30*time.Second, func(b Batch) bool {
		seen = seen || hasChange(b, root)
		done = done || b.HistoryDone
		return seen && done
	})
	if !seen {
		t.Error("change made before watching was not replayed")
	}
	if !done {
		t.Error("no HistoryDone after replay")
	}
	_ = os.Remove(filepath.Join(root, "while-closed.txt"))
}
