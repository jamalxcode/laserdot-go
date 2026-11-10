package core

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func corpus(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "media", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStory13ResolutionAndImageFilters(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "home")
	for name, wh := range map[string][2]uint32{
		"IMG_0001.png": {4032, 3024}, "IMG_0002.png": {3024, 4032}, "wallpaper-8k.png": {7680, 4320}, "thumb.png": {640, 480},
	} {
		writeAged(t, filepath.Join(root, "Pictures", name), pngHeader(wh[0], wh[1]))
	}
	writeAged(t, filepath.Join(root, "Pictures/broken.png"), []byte("not a png at all"))
	writeAged(t, filepath.Join(root, "Pictures/RAW/DSC_0001.NEF"), []byte("raw"))
	ld, _ := Open(filepath.Join(dir, "i.db"))
	defer ld.Close()
	ld.Index(root)
	expect(t, lnames(t, ld, "res:4k"), "IMG_0001.png", "IMG_0002.png")
	expect(t, lnames(t, ld, "res:8k"), "wallpaper-8k.png")
	expect(t, lnames(t, ld, "orientation:portrait"), "IMG_0002.png")
	expect(t, lnames(t, ld, "megapixels:>12"), "IMG_0001.png", "IMG_0002.png", "wallpaper-8k.png")
	expect(t, lnames(t, ld, "width:640"), "thumb.png")
	expect(t, lnames(t, ld, "raw:true"), "DSC_0001.NEF")
	r, _ := ld.Search("wallpaper", DefaultSearchOptions)
	if *r.Hits[0].Width != 7680 || *r.Hits[0].Height != 4320 {
		t.Error("dimensions")
	}
	// Story 22: unreadable media is still found by name.
	r, _ = ld.Search("broken", DefaultSearchOptions)
	if len(r.Hits) != 1 || r.Hits[0].Kind != "image" || r.Hits[0].Width != nil {
		t.Errorf("%+v", r.Hits)
	}
}

func TestReindexReusesProbesAndDefersFresh(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "lib")
	writeAged(t, filepath.Join(root, "Movies/trip.mp4"), corpus(t, "h264_4k.mp4"))
	writeAged(t, filepath.Join(root, "Docs/old-report.txt"), []byte("txt"))
	root, _ = filepath.EvalSymlinks(root)
	ld, _ := Open(filepath.Join(dir, "i.db"))
	defer ld.Close()
	st, _ := ld.Index(root)
	if st.Probed != 1 {
		t.Errorf("%+v", st)
	}
	r, _ := ld.Search("trip", DefaultSearchOptions)
	if m := r.Hits[0].Media; m == nil || *m.VideoCodec != "h264" || len(m.Audio) != 1 {
		t.Errorf("%+v", r.Hits[0].Media)
	}
	// A file written a moment ago may still be being written: probed later.
	os.WriteFile(filepath.Join(root, "Pictures/new.png"), pngHeader(1920, 1080), 0o644)
	os.MkdirAll(filepath.Join(root, "Pictures"), 0o755)
	os.WriteFile(filepath.Join(root, "Pictures/new.png"), pngHeader(1920, 1080), 0o644)
	os.Rename(filepath.Join(root, "Docs/old-report.txt"), filepath.Join(root, "Docs/renamed-report.txt"))
	st, _ = ld.Index(root)
	if !st.Incremental || st.Renamed != 1 || st.Added != 2 || st.Probed != 0 || st.Pending != 1 {
		t.Errorf("%+v", st)
	}
	expect(t, lnames(t, ld, "new"), "new.png")
	expect(t, lnames(t, ld, "res:1080p"))
	// Once stable, the next refresh reads it, and only it.
	past(t, filepath.Join(root, "Pictures/new.png"), time.Hour)
	st, _ = ld.Index(root)
	if st.Probed != 1 {
		t.Errorf("only the new image is read: %+v", st)
	}
	expect(t, lnames(t, ld, "res:1080p"), "new.png")
	// The 4K video was never re-read, and its facts survived.
	expect(t, lnames(t, ld, "res:4k codec:h264"), "trip.mp4")
}

func guideLibrary(t *testing.T) (string, *Laserdot) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	lib := filepath.Join(base, "lib")
	writeAged(t, filepath.Join(lib, "Movies/trip.mp4"), corpus(t, "h264_4k.mp4"))
	writeAged(t, filepath.Join(lib, "Music/ya rayah.mp3"), corpus(t, "song.mp3"))
	writeAged(t, filepath.Join(lib, "Docs/notes.txt"), []byte("hello"))
	os.MkdirAll(filepath.Join(base, "Elsewhere"), 0o755)
	ld, err := Open(filepath.Join(base, "data/index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ld.Close() })
	ld.Index(lib)
	return base, ld
}

func TestCoachingRelaxesTheFilterThatEmptiesTheResults(t *testing.T) {
	_, ld := guideLibrary(t)
	c := ld.Coach("trip res:8k")
	if !reflect.DeepEqual(c[0], Coaching{Kind: CoachRelax, Removed: "res:8k", Query: "trip", Count: 1}) {
		t.Errorf("%+v", c)
	}
	if len(c) > MaxCoaching || c[len(c)-1].Kind != CoachShowSyntax {
		t.Errorf("%+v", c)
	}
}

func TestCoachingCorrectsAMisspelledValue(t *testing.T) {
	_, ld := guideLibrary(t)
	c := ld.Coach("artist:fairuz")
	if !reflect.DeepEqual(c[0], Coaching{Kind: CoachFixValue, From: "fairuz", To: "fairouz", Query: "artist:fairouz", Count: 1}) {
		t.Errorf("%+v", c)
	}
	c = ld.Coach("ext:mpp4 trip")
	if c[0].Kind != CoachFixValue || c[0].To != "mp4" || c[0].Query != "trip ext:mp4" {
		t.Errorf("%+v", c)
	}
}

func TestCoachingOffersToIndexAFolder(t *testing.T) {
	base, ld := guideLibrary(t)
	q := "trip in:" + filepath.Join(base, "Elsewhere")
	if _, err := ld.Search(q, DefaultSearchOptions); err == nil {
		t.Error("not an indexed folder")
	}
	if c := ld.Coach(q); c[0] != (Coaching{Kind: CoachIndexFolder, Path: filepath.Join(base, "Elsewhere")}) {
		t.Errorf("%+v", c)
	}
	if c := ld.Coach("res:4l"); !reflect.DeepEqual(c, []Coaching{{Kind: CoachShowSyntax}}) {
		t.Errorf("%+v", c)
	}
}

func TestLiveCountsForEnumeratedValues(t *testing.T) {
	_, ld := guideLibrary(t)
	counts := ld.ValueCounts()
	get := func(k, v string) uint64 {
		for _, c := range counts {
			if c.Key == k && c.Value == v {
				return c.Count
			}
		}
		t.Fatalf("no count for %s:%s", k, v)
		return 0
	}
	if get("kind", "video") != 1 || get("kind", "audio") != 1 || get("res", "4k") != 1 || get("res", "8k") != 0 || get("hdr", "true") != 0 {
		t.Error(counts)
	}
	if len(counts) <= 50 {
		t.Error(len(counts))
	}
}
