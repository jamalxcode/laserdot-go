package core

import (
	"path/filepath"
	"testing"
)

// A plain word that names a file type means that type, like Spotlight
// (spec §2.7 type words).
func TestTypeWords(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "home")
	for _, rel := range []string{
		"beach.jpg", "Beach Party.JPEG", "jpg-notes.txt", "clip.avi", "david.txt", "avi plans.txt",
		"song.mp3", "holiday.mp4", "report.pdf", "pdf tips.txt", "deck.ppt", "Pitch.pptx", "ppt notes.txt",
		"budget.xlsx",
	} {
		write(t, root, rel, "x")
	}
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ld.Close() })
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	names := func(q string) []string {
		t.Helper()
		r, err := ld.Search(q, DefaultSearchOptions)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		out := []string{}
		for _, h := range r.Hits {
			out = append(out, h.Name)
		}
		return sorted(out)
	}
	eq(t, "jpg", names("jpg"), "Beach Party.JPEG", "beach.jpg")
	eq(t, "JPEG", names("JPEG"), "Beach Party.JPEG", "beach.jpg")
	eq(t, "avi", names("avi"), "clip.avi")
	eq(t, "beach jpg", names("beach jpg"), "Beach Party.JPEG", "beach.jpg")
	eq(t, "-jpg beach", names("beach -jpg"))
	eq(t, "jpg OR avi", names("jpg OR avi"), "Beach Party.JPEG", "beach.jpg", "clip.avi")
	eq(t, "videos", names("videos"), "clip.avi", "holiday.mp4")
	eq(t, "music", names("music"), "song.mp3")
	eq(t, "photos", names("photos"), "Beach Party.JPEG", "beach.jpg")
	eq(t, "صور", names("صور"), "Beach Party.JPEG", "beach.jpg")
	eq(t, "ppt", names("ppt"), "Pitch.pptx", "deck.ppt")
	eq(t, "powerpoint", names("powerpoint"), "Pitch.pptx", "deck.ppt")
	eq(t, "excel", names("excel"), "budget.xlsx")
	// Quotes or wildcards search the name instead.
	eq(t, `"avi"`, names(`"avi"`), "avi plans.txt", "clip.avi", "david.txt")
	eq(t, "pdf*", names("pdf*"), "pdf tips.txt")
}
