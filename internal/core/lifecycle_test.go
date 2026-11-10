package core

// Seam 1, file lifecycle (spec §2.6, stories 10–12): changes applied
// incrementally must leave the index exactly as a fresh index would.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/watch"
)

func allOpts(limit int) SearchOptions {
	return SearchOptions{Page: index.Page{Limit: limit}}
}

func lpaths(ld *Laserdot, q string) []string {
	r, err := ld.Search(q, allOpts(100_000))
	if err != nil {
		// Errors (an in: folder the index lacks) become a comparable value.
		return []string{"<error: " + err.Error() + ">"}
	}
	var v []string
	for _, h := range r.Hits {
		v = append(v, h.Path)
	}
	slices.Sort(v)
	return v
}

func lnames(t *testing.T, ld *Laserdot, q string) []string {
	t.Helper()
	r, err := ld.Search(q, allOpts(100_000))
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	v := []string{}
	for _, h := range r.Hits {
		v = append(v, h.Name)
	}
	slices.Sort(v)
	return v
}

func past(t *testing.T, p string, ago time.Duration) {
	tm := time.Now().Add(-ago)
	if err := os.Chtimes(p, tm, tm); err != nil {
		t.Fatal(err)
	}
}

// pngHeader is a minimal PNG (signature + IHDR).
func pngHeader(w, h uint32) []byte {
	v := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 13, 'I', 'H', 'D', 'R'}
	v = append(v, byte(w>>24), byte(w>>16), byte(w>>8), byte(w), byte(h>>24), byte(h>>16), byte(h>>8), byte(h))
	return append(v, 8, 2, 0, 0, 0, 0, 0, 0, 0)
}

func writeAged(t *testing.T, p string, data []byte) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	past(t, p, time.Hour)
}

type rng uint64

func (r *rng) next() uint64 {
	*r ^= *r << 13
	*r ^= *r >> 7
	*r ^= *r << 17
	return uint64(*r)
}

func (r *rng) below(n int) int { return int(r.next() % uint64(max(n, 1))) }

func walkTree(root string) (dirs, files []string) {
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == root {
			return nil
		}
		if d.IsDir() {
			dirs = append(dirs, p)
		} else {
			files = append(files, p)
		}
		return nil
	})
	return
}

type answer struct {
	q     string
	paths []string
}

// snapshotOf: queries whose answers must match between the incremental
// index and a fresh one.
func snapshotOf(ld *Laserdot, root string) []answer {
	qs := []string{"re:.", "kind:image", "res:4k", "ext:txt", "alpha", "beta OR gamma", "kind:folder", "fsize:>100b"}
	dirs, _ := walkTree(root)
	slices.Sort(dirs)
	for _, d := range dirs[:min(6, len(dirs))] {
		qs = append(qs, fmt.Sprintf("in:%q", d))
	}
	var out []answer
	for _, q := range qs {
		out = append(out, answer{q, lpaths(ld, q)})
	}
	return out
}

func TestRandomChangesMatchAFreshIndex(t *testing.T) {
	seeds := []uint64{0xDEADBEEFCAFEF00D, 1, 2, 3, 0x12345678, 42, 7, 99, 0xABCDEF, 2024, 31337, 0xFEED}
	// LASERDOT_STRESS_SEEDS=200 runs a longer local stress test.
	if n, _ := strconv.Atoi(os.Getenv("LASERDOT_STRESS_SEEDS")); n > 0 {
		for i := 0; i < n; i++ {
			seeds = append(seeds, 1000+uint64(i)*7919)
		}
	}
	if testing.Short() {
		seeds = seeds[:3]
	}
	for _, seed := range seeds {
		t.Run(fmt.Sprint(seed), func(t *testing.T) { randomRun(t, seed) })
	}
}

func randomRun(t *testing.T, seed uint64) {
	words := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}
	exts := []string{"txt", "png", "pdf", "md"}
	dir := t.TempDir()
	root := filepath.Join(dir, "tree")
	r := rng(seed | 1)
	for i := 0; i < 40; i++ {
		d := filepath.Join(root, words[i%3], words[(i/3)%6])
		ext := exts[r.below(len(exts))]
		data := []byte(strings.Repeat("x", r.below(300)))
		if ext == "png" {
			data = pngHeader(3840+uint32(i), 2160)
		}
		writeAged(t, filepath.Join(d, fmt.Sprintf("%s-%d.%s", words[r.below(6)], i, ext)), data)
	}
	root, _ = filepath.EvalSymlinks(root)
	ld, err := Open(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	var log []string
	pick := func(dirs []string) string {
		if len(dirs) == 0 {
			return root
		}
		return dirs[r.below(len(dirs))]
	}
	renamedTotal := uint64(0)
	for round := 0; round < 25; round++ {
		var touched []string
		for k := 0; k < 8; k++ {
			dirs, files := walkTree(root)
			switch r.below(8) {
			case 0, 1: // create a file
				d := root
				if len(dirs) > 0 && r.below(4) != 0 {
					d = dirs[r.below(len(dirs))]
				}
				ext := exts[r.below(len(exts))]
				p := filepath.Join(d, fmt.Sprintf("%s-n%d.%s", words[r.below(6)], r.below(100_000), ext))
				data := []byte(strings.Repeat("y", r.below(400)))
				if ext == "png" {
					data = pngHeader(4000, 3000)
				}
				writeAged(t, p, data)
				log = append(log, fmt.Sprintf("r%d create %s", round, p))
				touched = append(touched, d)
			case 2:
				if len(files) == 0 {
					continue
				}
				f := files[r.below(len(files))]
				os.Remove(f)
				log = append(log, fmt.Sprintf("r%d rm %s", round, f))
				touched = append(touched, filepath.Dir(f))
			case 3: // rename or move a file, sometimes changing its extension
				if len(files) == 0 {
					continue
				}
				f := files[r.below(len(files))]
				d := pick(dirs)
				ext := strings.TrimPrefix(filepath.Ext(f), ".")
				if r.below(3) == 0 {
					ext = exts[r.below(len(exts))]
				}
				to := filepath.Join(d, fmt.Sprintf("%s-m%d.%s", words[r.below(6)], r.below(100_000), ext))
				if err := os.Rename(f, to); err != nil {
					t.Fatal(err)
				}
				log = append(log, fmt.Sprintf("r%d mv %s -> %s", round, f, to))
				touched = append(touched, filepath.Dir(f), d)
			case 4: // modify contents
				if len(files) == 0 {
					continue
				}
				f := files[r.below(len(files))]
				data, _ := os.ReadFile(f)
				os.WriteFile(f, append(data, "more data"...), 0o644)
				past(t, f, time.Duration(1800+r.below(100))*time.Second)
				touched = append(touched, filepath.Dir(f))
			case 5:
				d := pick(dirs)
				nd := filepath.Join(d, fmt.Sprintf("%s-dir%d", words[r.below(6)], r.below(1000)))
				inside := filepath.Join(nd, fmt.Sprintf("inside-%d.txt", r.below(1000)))
				writeAged(t, inside, []byte("hello"))
				log = append(log, fmt.Sprintf("r%d mkdir+file %s", round, inside))
				touched = append(touched, d)
			case 6: // move a folder (not into itself)
				if len(dirs) == 0 {
					continue
				}
				src := dirs[r.below(len(dirs))]
				var cands []string
				for _, d := range dirs {
					if !within(d, src) {
						cands = append(cands, d)
					}
				}
				dst := pick(cands)
				to := filepath.Join(dst, fmt.Sprintf("moved-%d", r.below(100_000)))
				if err := os.Rename(src, to); err != nil {
					t.Fatal(err)
				}
				log = append(log, fmt.Sprintf("r%d mvdir %s -> %s", round, src, to))
				touched = append(touched, filepath.Dir(src), dst)
			case 7:
				if len(dirs) <= 3 {
					continue
				}
				d := dirs[r.below(len(dirs))]
				os.RemoveAll(d)
				log = append(log, fmt.Sprintf("r%d rmdir %s", round, d))
				touched = append(touched, filepath.Dir(d))
			}
		}
		// Folder-level change signals, like FSEvents delivers. Every few
		// rounds, a recursive refresh instead (dropped events).
		var changes []Change
		if round%5 == 4 {
			changes = []Change{{Path: root, Recursive: true}}
		} else {
			for _, p := range touched {
				changes = append(changes, Change{Path: p})
			}
		}
		stats, err := ld.ApplyChanges(changes)
		if err != nil {
			t.Fatal(err)
		}
		renamedTotal += stats.Renamed

		freshDB := filepath.Join(dir, fmt.Sprintf("fresh-%d.db", round))
		fresh, err := Open(freshDB)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fresh.Index(root); err != nil {
			t.Fatal(err)
		}
		want := snapshotOf(fresh, root)
		check := func(what string, got []answer) {
			for i := range got {
				if !reflect.DeepEqual(got[i].paths, want[i].paths) {
					var extra, missing []string
					for _, p := range got[i].paths {
						if !slices.Contains(want[i].paths, p) {
							extra = append(extra, p)
						}
					}
					for _, p := range want[i].paths {
						if !slices.Contains(got[i].paths, p) {
							missing = append(missing, p)
						}
					}
					t.Fatalf("round %d (%s): `%s` differs from a fresh index\n  extra:   %q\n  missing: %q\n  stats: %+v\nlog:\n%s",
						round, what, want[i].q, extra, missing, stats, strings.Join(log, "\n"))
				}
			}
		}
		check("live", snapshotOf(ld, root))
		// Every other round the live index switches to a memory-mapped
		// snapshot of itself, so later rounds also apply live updates on
		// mapped columns (copy-on-write pages, appended tails).
		if round%2 == 1 {
			if err := ld.Remap(); err != nil {
				t.Fatal(err)
			}
			if !ld.Stats().FromSnapshot {
				t.Fatalf("round %d: live index not mapped", round)
			}
			check("mapped", snapshotOf(ld, root))
		}
		// The durable store must agree too.
		reopened, err := Open(filepath.Join(dir, "live.db"))
		if err != nil {
			t.Fatal(err)
		}
		check("reopened", snapshotOf(reopened, root))
		reopened.Close()
		fresh.Close()
		os.Remove(freshDB)
	}
	if renamedTotal <= 5 {
		t.Errorf("renames should be tracked in place (%d)", renamedTotal)
	}
}

func TestRenameAndMoveKeepMediaWithoutReprobing(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "lib")
	writeAged(t, filepath.Join(root, "in/clip.png"), pngHeader(3840, 2160))
	writeAged(t, filepath.Join(root, "in/a/b/deep.txt"), []byte("deep"))
	root, _ = filepath.EvalSymlinks(root)
	ld, _ := Open(filepath.Join(dir, "i.db"))
	defer ld.Close()
	ld.Index(root)
	os.Mkdir(filepath.Join(root, "out"), 0o755)
	os.Rename(filepath.Join(root, "in/clip.png"), filepath.Join(root, "out/renamed.png"))
	os.Rename(filepath.Join(root, "in/a"), filepath.Join(root, "out/a-moved"))
	s, err := ld.ApplyChanges([]Change{{Path: filepath.Join(root, "in")}, {Path: filepath.Join(root, "out")}})
	if err != nil || s.Renamed != 2 || s.Probed != 0 {
		t.Fatalf("%+v %v", s, err)
	}
	if got := lnames(t, ld, fmt.Sprintf("in:%q", filepath.Join(root, "out"))); !reflect.DeepEqual(got, []string{"a-moved", "b", "deep.txt", "renamed.png"}) {
		t.Error(got)
	}
	if got := lnames(t, ld, fmt.Sprintf("in:%q", filepath.Join(root, "in"))); len(got) != 0 {
		t.Error(got)
	}
	if got := lpaths(ld, "deep"); !reflect.DeepEqual(got, []string{filepath.Join(root, "out/a-moved/b/deep.txt")}) {
		t.Error(got)
	}
}

func TestHalfDownloadedFilesAppearWhenComplete(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "dl")
	writeAged(t, filepath.Join(root, "movie.mkv.crdownload"), []byte("partial"))
	writeAged(t, filepath.Join(root, "song.mp3.part"), []byte("partial"))
	root, _ = filepath.EvalSymlinks(root)
	ld, _ := Open(filepath.Join(dir, "i.db"))
	defer ld.Close()
	ld.Index(root)
	if len(lnames(t, ld, "movie")) != 0 || len(lnames(t, ld, "song")) != 0 {
		t.Fatal("partial downloads indexed")
	}
	os.Rename(filepath.Join(root, "movie.mkv.crdownload"), filepath.Join(root, "movie.mkv"))
	ld.ApplyChanges([]Change{{Path: root}})
	if got := lnames(t, ld, "movie"); !reflect.DeepEqual(got, []string{"movie.mkv"}) {
		t.Error(got)
	}
}

func TestHardLinksAreListedOnce(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "h")
	writeAged(t, filepath.Join(root, "a/report.pdf"), []byte("pdf"))
	os.MkdirAll(filepath.Join(root, "b"), 0o755)
	if err := os.Link(filepath.Join(root, "a/report.pdf"), filepath.Join(root, "b/report-link.pdf")); err != nil {
		t.Skip(err)
	}
	ld, _ := Open(filepath.Join(dir, "i.db"))
	defer ld.Close()
	ld.Index(root)
	if got := lnames(t, ld, "report"); !reflect.DeepEqual(got, []string{"report.pdf"}) {
		t.Error(got)
	}
}

func TestOfflineFolderKeptThenPurgedAfter30Days(t *testing.T) {
	dir := t.TempDir()
	drive := filepath.Join(dir, "drive")
	writeAged(t, filepath.Join(drive, "film.mkv"), []byte("x"))
	drive, _ = filepath.EvalSymlinks(drive)
	db := filepath.Join(dir, "i.db")
	ld, _ := Open(db)
	ld.Index(drive)
	os.Rename(drive, filepath.Join(dir, "elsewhere")) // "unplug"
	s, err := ld.Refresh()
	if err != nil || !reflect.DeepEqual(s.OfflineRoots, []string{drive}) {
		t.Fatalf("%+v %v", s, err)
	}
	if got := lnames(t, ld, "film"); !reflect.DeepEqual(got, []string{"film.mkv"}) {
		t.Error("entries survive while offline", got)
	}
	if roots, _ := ld.Roots(); !roots[0].Offline {
		t.Error("offline flag")
	}
	ld.Close()
	ld, _ = OpenAt(db, time.Now().Add(10*24*time.Hour))
	if len(ld.OpenReport().Purged) != 0 {
		t.Error("10 days: still kept")
	}
	ld.Close()
	ld, _ = OpenAt(db, time.Now().Add(31*24*time.Hour))
	defer ld.Close()
	if !reflect.DeepEqual(ld.OpenReport().Purged, []string{drive}) || len(lnames(t, ld, "film")) != 0 {
		t.Errorf("%+v", ld.OpenReport())
	}
}

func TestCorruptDatabaseIsQuarantinedAndRebuilt(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "docs")
	writeAged(t, filepath.Join(root, "invoice.pdf"), []byte("pdf"))
	root, _ = filepath.EvalSymlinks(root)
	db := filepath.Join(dir, "i.db")
	ld, _ := Open(db)
	ld.Index(root)
	ld.Close()
	os.Remove(db + "-wal")
	os.Remove(db + "-shm")
	b, _ := os.ReadFile(db)
	for i := 100; i < len(b); i++ {
		b[i] = 0xA5
	}
	os.WriteFile(db, b, 0o600)
	ld, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	r := ld.OpenReport()
	if r.Quarantined == nil || !reflect.DeepEqual(r.Reindexed, []string{root}) {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(*r.Quarantined); err != nil {
		t.Error(err)
	}
	if got := lnames(t, ld, "invoice"); !reflect.DeepEqual(got, []string{"invoice.pdf"}) {
		t.Error(got)
	}
}

func TestLiveWatcherKeepsTheIndexCurrent(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "w")
	writeAged(t, filepath.Join(root, "old.txt"), []byte("x"))
	root, _ = filepath.EvalSymlinks(root)
	ld, _ := Open(filepath.Join(dir, "i.db"))
	defer ld.Close()
	ld.Index(root)
	w := ld.Watch(watch.Options{Latency: 50 * time.Millisecond, PollInterval: 200 * time.Millisecond}, WatchEvents{})
	defer w.Stop()
	waitFor := func(q string, want []string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			got := lnames(t, ld, q)
			if reflect.DeepEqual(got, want) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %q, want %q (backend %s)", q, got, want, w.Backend())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	time.Sleep(300 * time.Millisecond) // let the watcher start
	writeAged(t, filepath.Join(root, "sub", "fresh.txt"), []byte("y"))
	waitFor("fresh", []string{"fresh.txt"})
	os.Rename(filepath.Join(root, "old.txt"), filepath.Join(root, "renamed.txt"))
	waitFor("renamed", []string{"renamed.txt"})
	waitFor("old", []string{})
	// A folder indexed later is watched too.
	other := filepath.Join(dir, "other")
	writeAged(t, filepath.Join(other, "a.txt"), []byte("a"))
	ld.Index(other)
	w.RootsChanged()
	time.Sleep(700 * time.Millisecond)
	writeAged(t, filepath.Join(other, "later.txt"), []byte("b"))
	waitFor("later", []string{"later.txt"})
}
