package core

import (
	"archive/zip"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeZip(t *testing.T, p string, names ...string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, n := range names {
		fw, err := w.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte("data of " + n))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	past(t, p, 3600e9)
}

// The owner chose "search inside zip files": a word matching a file inside
// a zip finds the zip, after the files named with it, with what matched.
func TestAWordFindsTheZipHoldingAFileNamedWithIt(t *testing.T) {
	ld, root := newCore(t)
	writeAged(t, filepath.Join(root, "docs/invoice-april.pdf"), []byte("%PDF"))
	writeZip(t, filepath.Join(root, "old/taxes 2023.zip"), "2023/invoice-march.pdf", "2023/receipt.jpg", "__MACOSX/2023/._invoice-march.pdf")
	writeZip(t, filepath.Join(root, "old/photos.zip"), "beach.jpg")
	os.WriteFile(filepath.Join(root, "old/broken.zip"), []byte("not a zip"), 0o644)
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	st, err := ld.UpdateArchives()
	if err != nil || st.Listed != 2 || st.Unreadable != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	r, err := ld.Search("invoice", DefaultSearchOptions)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, h := range r.Hits {
		names = append(names, h.Name)
	}
	if !slices.Equal(names, []string{"invoice-april.pdf", "taxes 2023.zip"}) || r.Total != 2 {
		t.Fatalf("the file, then the zip holding one: %v (total %d)", names, r.Total)
	}
	if got := r.Hits[1].Inside; !slices.Equal(got, []string{"2023/invoice-march.pdf"}) {
		t.Errorf("the zip says what matched (not the __MACOSX copy): %v", got)
	}
	// Two words: one file inside must have both.
	if r, _ := ld.Search("invoice receipt", DefaultSearchOptions); r.Total != 0 {
		t.Errorf("no file inside is named with both words: %+v", r.Hits)
	}
	if r, _ := ld.Search("invoice march", DefaultSearchOptions); r.Total != 1 || r.Hits[0].Name != "taxes 2023.zip" {
		t.Errorf("one file inside has both: %+v", r.Hits)
	}
	// Filters apply to the zip itself.
	if r, _ := ld.Search("beach kind:archive", DefaultSearchOptions); r.Total != 1 {
		t.Errorf("beach inside photos.zip, an archive: %+v", r.Hits)
	}
	if r, _ := ld.Search("beach kind:image", DefaultSearchOptions); r.Total != 0 {
		t.Errorf("a zip is not an image: %+v", r.Hits)
	}
	// A zip that changes is listed again; one that goes is forgotten.
	writeZip(t, filepath.Join(root, "old/photos.zip"), "sunset.jpg")
	os.Remove(filepath.Join(root, "old/taxes 2023.zip"))
	if _, err := ld.Refresh(); err != nil {
		t.Fatal(err)
	}
	if st, err := ld.UpdateArchives(); err != nil || st.Listed != 1 || st.Removed != 1 {
		t.Fatalf("after changes: %+v %v", st, err)
	}
	if r, _ := ld.Search("sunset", DefaultSearchOptions); r.Total != 1 {
		t.Errorf("the new listing: %+v", r.Hits)
	}
	if r, _ := ld.Search("invoice", DefaultSearchOptions); r.Total != 1 {
		t.Errorf("the deleted zip is gone: %+v", r.Hits)
	}
	// The listings survive a restart.
	ld.Close()
	ld2, err := Open(ld.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer ld2.Close()
	if r, _ := ld2.Search("sunset", DefaultSearchOptions); r.Total != 1 {
		t.Errorf("after reopening: %+v", r.Hits)
	}
}
