package core

// Memory-mapped snapshot (§2.1.1g) at Seam 1: an index loaded from the
// snapshot answers every query exactly like one rebuilt from SQLite, keeps
// working with live updates, and a stale or damaged snapshot is never used.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/index"
)

var snapQueries = []string{"trip", "invoice march", "فاتوره", "report*", "re:^song", "ivnoice", "kind:video", "res:4k",
	"hdr:true", "codec:hevc", "a(lang:ar channels:5.1)", "artist:fairouz", "lossless:true", "fsize:>1kb", "ext:txt,md",
	"-kind:folder", ".hidden", "duration:<10s", "orientation:portrait"}

func snapResults(t *testing.T, ld *Laserdot, root string) []string {
	t.Helper()
	var out []string
	for _, q := range snapQueries {
		for _, srt := range []index.Sort{index.Relevance, index.BySize, index.ByModified, index.ByName} {
			r, err := ld.Search(q, SearchOptions{Page: index.Page{Sort: srt, Limit: 1000}, TypoFallback: true})
			if err != nil {
				t.Fatalf("%s: %v", q, err)
			}
			var names []string
			for _, h := range r.Hits {
				names = append(names, strings.TrimPrefix(h.Path, root+"/"))
			}
			if srt == index.Relevance {
				slices.Sort(names) // ties may order differently; the set must match
			}
			out = append(out, fmt.Sprintf("%s / %v: %q", q, srt, names))
		}
	}
	return out
}

type snapLib struct{ root, db string }

func snapLibrary(t *testing.T) snapLib {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "lib")
	age := 3600
	put := func(rel string, data []byte) {
		writeAged(t, filepath.Join(root, rel), data)
		age += 10 // distinct times: date sorts have no ties
		past(t, filepath.Join(root, rel), time.Duration(age)*time.Second)
	}
	put("Movies/trip.mp4", corpus(t, "h264_4k.mp4"))
	put("Movies/concert.mkv", corpus(t, "multi.mkv"))
	put("Movies/hdr.mp4", corpus(t, "hevc10_pq.mp4"))
	put("Movies/phone.mov", corpus(t, "rotated.mov"))
	put("Music/song.flac", corpus(t, "song.flac"))
	put("Music/song.mp3", corpus(t, "song.mp3"))
	put("Docs/invoice-march.txt", []byte("x"))
	put("Docs/report-2024.md", []byte("xx"))
	put("Docs/report final.md", []byte("xxxx"))
	put("Docs/فاتورة ٢٠٢٤.txt", []byte("xxx"))
	put("Docs/.hidden/secret.txt", []byte("sssss"))
	put("Photos/IMG_0001.jpg", corpus(t, "base.jpg"))
	return snapLib{root, filepath.Join(base, "data/index.db")}
}

func mustOpen(t *testing.T, db string) *Laserdot {
	t.Helper()
	ld, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	return ld
}

func TestSnapshotAnswersLikeSQLite(t *testing.T) {
	lib := snapLibrary(t)
	ld := mustOpen(t, lib.db)
	ld.Index(lib.root)
	want := snapResults(t, ld, lib.root)
	if err := ld.Remap(); err != nil {
		t.Fatal(err)
	}
	if !ld.Stats().FromSnapshot {
		t.Error("served from the snapshot")
	}
	if wrote, _ := ld.Checkpoint(); wrote {
		t.Error("nothing changed: no rewrite")
	}
	if got := snapResults(t, ld, lib.root); !reflect.DeepEqual(got, want) {
		t.Errorf("mapped differs:\n%q\n%q", got, want)
	}
	ld.Close()
	ld = mustOpen(t, lib.db)
	defer ld.Close()
	if st := ld.Stats(); !st.FromSnapshot || st.MappedBytes == 0 {
		t.Errorf("%+v", st)
	}
	if got := snapResults(t, ld, lib.root); !reflect.DeepEqual(got, want) {
		t.Errorf("reopened differs")
	}
}

func TestAMappedIndexTakesLiveUpdates(t *testing.T) {
	lib := snapLibrary(t)
	ld := mustOpen(t, lib.db)
	ld.Index(lib.root)
	ld.Checkpoint()
	ld.Close()
	ld = mustOpen(t, lib.db)
	if !ld.Stats().FromSnapshot {
		t.Fatal("not mapped")
	}
	writeAged(t, filepath.Join(lib.root, "Docs/invoice-april.txt"), []byte("new file"))
	os.Rename(filepath.Join(lib.root, "Docs/report final.md"), filepath.Join(lib.root, "Docs/report renamed.md"))
	os.MkdirAll(filepath.Join(lib.root, "Archive"), 0o755)
	os.Rename(filepath.Join(lib.root, "Music"), filepath.Join(lib.root, "Archive/Music"))
	writeAged(t, filepath.Join(lib.root, "Docs/report-2024.md"), []byte("edited and longer"))
	os.Remove(filepath.Join(lib.root, "Movies/hdr.mp4"))
	if _, err := ld.Refresh(); err != nil {
		t.Fatal(err)
	}
	live := snapResults(t, ld, lib.root)
	fresh := mustOpen(t, filepath.Join(filepath.Dir(lib.db), "fresh.db"))
	fresh.Index(lib.root)
	if !reflect.DeepEqual(live, snapResults(t, fresh, lib.root)) {
		t.Error("live updates on a mapped store differ from a fresh index")
	}
	fresh.Close()
	// The changed state round-trips through a new snapshot (overlay and all).
	if wrote, err := ld.Checkpoint(); !wrote || err != nil {
		t.Fatal(wrote, err)
	}
	ld.Close()
	ld = mustOpen(t, lib.db)
	defer ld.Close()
	if !ld.Stats().FromSnapshot || !reflect.DeepEqual(snapResults(t, ld, lib.root), live) {
		t.Error("overlay round trip")
	}
}

func TestAStaleSnapshotIsNotUsed(t *testing.T) {
	lib := snapLibrary(t)
	ld := mustOpen(t, lib.db)
	ld.Index(lib.root)
	ld.Checkpoint()
	writeAged(t, filepath.Join(lib.root, "Docs/late.txt"), []byte("late"))
	ld.Refresh()
	ld.Close()
	ld = mustOpen(t, lib.db)
	defer ld.Close()
	if ld.Stats().FromSnapshot {
		t.Error("the database changed since the snapshot")
	}
	if r, _ := ld.Search("late", DefaultSearchOptions); len(r.Hits) != 1 {
		t.Error("late")
	}
}

func TestADamagedSnapshotIsRefused(t *testing.T) {
	lib := snapLibrary(t)
	ld := mustOpen(t, lib.db)
	ld.Index(lib.root)
	want := snapResults(t, ld, lib.root)
	ld.Checkpoint()
	ld.Close()
	snap := index.SnapshotPath(lib.db)
	good, _ := os.ReadFile(snap)
	damaged := [][]byte{good[:len(good)/2]}
	for _, at := range []int{len(good) / 3, len(good) / 2, len(good) * 2 / 3} {
		d := slices.Clone(good)
		for i := at; i < min(at+64, len(good)-8); i++ {
			d[i] = 0xFF
		}
		damaged = append(damaged, d)
	}
	for _, d := range damaged {
		os.WriteFile(snap, d, 0o600)
		ld := mustOpen(t, lib.db)
		if !reflect.DeepEqual(snapResults(t, ld, lib.root), want) {
			t.Error("damaged snapshot changed the answers")
		}
		ld.Close()
	}
	os.WriteFile(snap, good[:len(good)-100], 0o600)
	if ld := mustOpen(t, lib.db); ld.Stats().FromSnapshot {
		t.Error("truncated file used")
	} else {
		ld.Close()
	}
	for i := 0; i < 60; i++ {
		d := slices.Clone(good)
		d[24+(i*7919)%(len(good)-24)] ^= 0x5A
		os.WriteFile(snap, d, 0o600)
		ld := mustOpen(t, lib.db)
		for _, q := range snapQueries {
			ld.Search(q, DefaultSearchOptions)
		}
		ld.Close()
	}
	os.WriteFile(snap, good, 0o600)
	ld = mustOpen(t, lib.db)
	defer ld.Close()
	if !ld.Stats().FromSnapshot {
		t.Error("the good snapshot maps again")
	}
}
