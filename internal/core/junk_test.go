package core

import (
	"path/filepath"
	"slices"
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
	// In the background (it must not hold the start up): soon gone.
	waitFor(t, "the old .DS_Store to go", func() bool { return len(dnames(t, ld2, "ds_store")) == 0 })
	expect(t, dnames(t, ld2, "plan"), "plan.txt")
}

// A drive indexed before system files were ignored loses them when it is
// attached again.
func TestADrivesOlderIndexLosesItsJunkFiles(t *testing.T) {
	saved := scan.SystemJunkNames
	scan.SystemJunkNames = nil
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Archive")
	writeAged(t, filepath.Join(drive, "Films/.DS_Store"), []byte("x"))
	if _, err := m.ld.CreateDriveIndex(drive, DriveOptions{}); err != nil {
		t.Fatal(err)
	}
	expect(t, dnames(t, m.ld, "ds_store"), ".DS_Store")
	scan.SystemJunkNames = saved
	if !m.ld.DetachDrive(drive) {
		t.Fatal("detach")
	}
	if _, err := m.ld.AttachDrive(drive, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the drive's old .DS_Store to go", func() bool { return len(dnames(t, m.ld, "ds_store")) == 0 })
	expect(t, dnames(t, m.ld, "trip"), "trip.mp4")
}

// Document packages are not results (apps are) (the owner: "fix it like
// Spotlight", after the pictures inside Ulysses sheets flooded a search for
// screenshots); an index made before loses what it held inside them.
func TestDocumentPackagesAreNotResults(t *testing.T) {
	saved := scan.DefaultOpaqueExts
	scan.DefaultOpaqueExts = slices.DeleteFunc(slices.Clone(saved), func(e string) bool { return e == "ulysses" || e == "rtfd" })
	ld, root := newCore(t)
	for _, n := range []string{
		"Writing/a1b2c3.ulysses/Media/Screenshot 2023.png", "Writing/a1b2c3.ulysses/Content.xml",
		"Writing/Letter.rtfd/TXT.rtf", "Writing/Screenshot kept.png",
	} {
		writeAged(t, filepath.Join(root, n), []byte("x"))
	}
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	expect(t, dnames(t, ld, "screenshot"), "Screenshot 2023.png", "Screenshot kept.png") // as before
	ld.db.SetSetting(junkCleanedKey, "1")                                                // cleared under the old rules
	db := ld.DBPath()
	ld.Close()
	scan.DefaultOpaqueExts = saved
	ld2, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer ld2.Close()
	waitFor(t, "the pictures inside the sheet to go", func() bool { return len(dnames(t, ld2, "screenshot")) == 1 })
	expect(t, dnames(t, ld2, "screenshot"), "Screenshot kept.png")
	// Not even the sheet itself is a result (the owner: "do not include app
	// packages, at all"); apps still are.
	expect(t, dnames(t, ld2, "a1b2c3"))
	expect(t, dnames(t, ld2, "txt"))
	expect(t, dnames(t, ld2, "letter"))
	// A sheet made later, and a change reported inside one: still nothing.
	writeAged(t, filepath.Join(root, "Writing/later.ulysses/Content.xml"), []byte("x"))
	if _, err := ld2.ApplyChanges([]Change{{Path: filepath.Join(root, "Writing")}, {Path: filepath.Join(root, "Writing/later.ulysses")},
		{Path: filepath.Join(root, "Writing/later.ulysses/Content.xml")}}); err != nil {
		t.Fatal(err)
	}
	expect(t, dnames(t, ld2, "later"))
	expect(t, dnames(t, ld2, "content"))
	// A fresh index never takes them in.
	ld3, root3 := newCore(t)
	writeAged(t, filepath.Join(root3, "Notes/x.ulysses/Media/Screenshot.png"), []byte("x"))
	if _, err := ld3.Index(root3); err != nil {
		t.Fatal(err)
	}
	expect(t, dnames(t, ld3, "screenshot"))
}
