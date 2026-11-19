package index

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
