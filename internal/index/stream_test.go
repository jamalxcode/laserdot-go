package index

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/scan"
)

func streamTree(t *testing.T) string {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	for a := 0; a < 6; a++ {
		for b := 0; b < 5; b++ {
			for f := 0; f < 8; f++ {
				p := filepath.Join(root, fmt.Sprintf("a%d/b%d/file %d.txt", a, b, f))
				os.MkdirAll(filepath.Dir(p), 0o755)
				os.WriteFile(p, []byte("x"), 0o644)
			}
		}
	}
	os.MkdirAll(filepath.Join(root, "hot/inside"), 0o755)
	os.WriteFile(filepath.Join(root, "hot/inside/wanted.txt"), []byte("y"), 0o644)
	os.MkdirAll(filepath.Join(root, "empty"), 0o755)
	return root
}

func openTemp(t *testing.T, name string) *Db {
	t.Helper()
	db, _, err := Open(filepath.Join(t.TempDir(), name, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// paths: every indexed path below root, sorted (and how many rows there are).
func paths(t *testing.T, db *Db) ([]string, int) {
	t.Helper()
	raw, err := db.Load()
	if err != nil {
		t.Fatal(err)
	}
	s := Build(raw)
	var out []string
	it := s.Live().Iterator()
	for it.HasNext() {
		out = append(out, s.Path(it.Next()))
	}
	slices.Sort(out)
	return out, len(raw)
}

// Streaming gives exactly the index the bulk path gives, with a tiny memory
// queue (most folders wait in the index) and small batches.
func TestStreamingMatchesTheBulkIndex(t *testing.T) {
	root := streamTree(t)
	bulk := openTemp(t, "bulk")
	if _, err := IndexRoot(bulk, root, scan.Options{}, BulkOptions{}); err != nil {
		t.Fatal(err)
	}
	want, _ := paths(t, bulk)

	db := openTemp(t, "stream")
	st, err := IndexStreaming(db, root, scan.Options{}, StreamOptions{MaxQueue: 3, BatchRows: 20})
	if err != nil {
		t.Fatal(err)
	}
	got, rows := paths(t, db)
	if !slices.Equal(got, want) {
		t.Fatalf("streaming found %d entries, bulk %d", len(got), len(want))
	}
	if rows != len(want) || st.Files != 241 {
		t.Fatalf("rows %d (want %d), files %d", rows, len(want), st.Files)
	}
	if db.HasUnlisted(root) {
		t.Fatal("folders left unlisted after a complete run")
	}
}

// Cut short (an unplugged drive), it goes on from where it stopped: nothing
// missing, nothing twice.
func TestStreamingResumesWithoutDuplicates(t *testing.T) {
	root := streamTree(t)
	db := openTemp(t, "stream")
	_, err := IndexStreaming(db, root, scan.Options{}, StreamOptions{MaxQueue: 4, BatchRows: 15, StopAfterBatches: 3})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected an interruption, got %v", err)
	}
	if !db.HasUnlisted(root) {
		t.Fatal("an interrupted run left no work to resume")
	}
	partial, _ := paths(t, db)
	st, err := IndexStreaming(db, root, scan.Options{}, StreamOptions{MaxQueue: 4, BatchRows: 15})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Resumed {
		t.Fatal("the second run started over instead of resuming")
	}
	got, rows := paths(t, db)
	bulk := openTemp(t, "bulk")
	IndexRoot(bulk, root, scan.Options{}, BulkOptions{})
	want, _ := paths(t, bulk)
	if !slices.Equal(got, want) || rows != len(want) {
		t.Fatalf("after resuming: %d entries in %d rows, want %d (had %d before)", len(got), rows, len(want), len(partial))
	}
}

// A folder with priority (one being searched in) is read before the rest.
func TestStreamingReadsPriorityFoldersFirst(t *testing.T) {
	root := streamTree(t)
	db := openTemp(t, "stream")
	var order []string
	_, err := IndexStreaming(db, root, scan.Options{}, StreamOptions{
		MaxWorkers: 1, BatchRows: 1,
		Priority: func(p string) float64 {
			if strings.Contains(p, "/hot") {
				return 100
			}
			return 0
		},
		OnBatch: func(rows []StreamRow) {
			for _, r := range rows {
				order = append(order, r.Raw.Name)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wanted, firstDeep := slices.Index(order, "wanted.txt"), slices.Index(order, "file 0.txt")
	if wanted < 0 || firstDeep < 0 || wanted > firstDeep {
		t.Fatalf("the searched folder was not read first (wanted at %d, other files from %d)", wanted, firstDeep)
	}
}

// A drive pulled out mid-index: the run stops, and the folders it could no
// longer read are not saved as empty. Plugged back in, it finishes.
func TestStreamingStopsWhenTheDriveGoesAway(t *testing.T) {
	root := streamTree(t)
	away := root + "-unplugged"
	db := openTemp(t, "stream")
	moved := false
	_, err := IndexStreaming(db, root, scan.Options{}, StreamOptions{
		MaxWorkers: 1, BatchRows: 1,
		Progress: func(uint64) {
			if !moved {
				moved = true
				os.Rename(root, away)
			}
		},
	})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected a stop, got %v", err)
	}
	os.Rename(away, root)
	st, err := IndexStreaming(db, root, scan.Options{}, StreamOptions{})
	if err != nil || !st.Resumed {
		t.Fatalf("resume: %v (resumed %v)", err, st.Resumed)
	}
	got, rows := paths(t, db)
	bulk := openTemp(t, "bulk")
	IndexRoot(bulk, root, scan.Options{}, BulkOptions{})
	want, _ := paths(t, bulk)
	if !slices.Equal(got, want) || rows != len(want) {
		t.Fatalf("after the drive came back: %d entries in %d rows, want %d", len(got), rows, len(want))
	}
}

// However many readers there are, no more than ProbeSlots files are open
// for their headers at once (a big drive full of videos must not open
// hundreds of files together and slow the whole Mac).
func TestStreamingOpensFewFilesAtOnce(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	for d := 0; d < 12; d++ {
		for f := 0; f < 60; f++ {
			p := filepath.Join(root, fmt.Sprintf("d%d/clip %d.mp4", d, f))
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte("not really a video"), 0o644)
			old := time.Now().Add(-time.Hour)
			os.Chtimes(p, old, old)
		}
	}
	var open, most atomic.Int64
	old := streamProbe
	streamProbe = func(e *scan.Entry) Probe {
		n := open.Add(1)
		for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
		}
		time.Sleep(time.Millisecond)
		open.Add(-1)
		return Probe{}
	}
	defer func() { streamProbe = old }()
	db := openTemp(t, "stream")
	if _, err := IndexStreaming(db, root, scan.Options{}, StreamOptions{MaxWorkers: 16}); err != nil {
		t.Fatal(err)
	}
	if most.Load() == 0 || most.Load() > int64(ProbeSlots()) {
		t.Fatalf("%d files open at once (limit %d)", most.Load(), ProbeSlots())
	}
}

// Paused (an update being installed), it reads nothing new; resumed, it
// finishes normally.
func TestStreamingWaitsWhilePaused(t *testing.T) {
	root := streamTree(t)
	db := openTemp(t, "stream")
	start := time.Now()
	var first time.Duration
	_, err := IndexStreaming(db, root, scan.Options{}, StreamOptions{
		Pause: func() bool { return time.Since(start) < 300*time.Millisecond },
		OnBatch: func([]StreamRow) {
			if first == 0 {
				first = time.Since(start)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first < 300*time.Millisecond {
		t.Errorf("read while paused (first batch after %v)", first)
	}
	if db.HasUnlisted(root) {
		t.Error("finished after resuming")
	}
}

// A run whose last folders are all empty still finishes (they were held
// back, as batches with no rows, and the run waited for ever).
func TestStreamingFinishesOnEmptyFolders(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	for i := 0; i < 30; i++ {
		os.MkdirAll(filepath.Join(root, fmt.Sprintf("empty%02d/deeper", i)), 0o755)
	}
	db := openTemp(t, "stream")
	done := make(chan error, 1)
	go func() {
		_, err := IndexStreaming(db, root, scan.Options{}, StreamOptions{BatchRows: 1000})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a tree of empty folders never finished")
	}
	if db.HasUnlisted(root) {
		t.Error("every folder listed")
	}
}
