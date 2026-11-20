package core

import (
	"path/filepath"
	"testing"

	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// The owner: "i keep getting .DS_Store that is an internal file that no one
// is interested in, can we ignore this file and other similar files".
func TestSystemJunkFilesAreNeverResults(t *testing.T) {
	ld, root := newCore(t)
	for _, n := range []string{"Photos/.DS_Store", "Photos/._beach.jpg", "Photos/Thumbs.db", "Photos/desktop.ini", ".localized"} {
		writeAged(t, filepath.Join(root, n), []byte("x"))
	}
	writeAged(t, filepath.Join(root, "Photos/beach.jpg"), []byte("jpg"))
	writeAged(t, filepath.Join(root, "Photos/.env"), []byte("a dot file people do look for"))
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"ds_store", "beach", "thumbs", "desktop", "localized"} {
		r, err := ld.Search(q+" hidden:true OR "+q, DefaultSearchOptions)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range r.Hits {
			if scan.IsSystemJunk(h.Name) {
				t.Errorf("%s: %s is a result", q, h.Path)
			}
		}
	}
	expect(t, dnames(t, ld, "beach"), "beach.jpg")
	expect(t, dnames(t, ld, "env"), ".env")
	// One made later, as Finder does when a folder is opened: still none.
	writeAged(t, filepath.Join(root, "Photos/Sub/.DS_Store"), []byte("x"))
	if _, err := ld.ApplyChanges([]Change{{Path: filepath.Join(root, "Photos"), Recursive: true}}); err != nil {
		t.Fatal(err)
	}
	expect(t, dnames(t, ld, "ds_store"))
}

// An index made before junk was ignored is cleared of it once, on opening.
func TestAnOlderIndexLosesItsJunkFiles(t *testing.T) {
	saved := scan.SystemJunkNames
	scan.SystemJunkNames = nil // as before: .DS_Store indexed
	ld, root := newCore(t)
	writeAged(t, filepath.Join(root, "Work/.DS_Store"), []byte("x"))
	writeAged(t, filepath.Join(root, "Work/plan.txt"), []byte("x"))
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	expect(t, dnames(t, ld, "ds_store"), ".DS_Store")
	ld.db.SetSetting(junkCleanedKey, "") // an index from before this version
	db := ld.DBPath()
	ld.Close()
	scan.SystemJunkNames = saved
	ld2, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer ld2.Close()
	expect(t, dnames(t, ld2, "ds_store"))
	expect(t, dnames(t, ld2, "plan"), "plan.txt")
}
