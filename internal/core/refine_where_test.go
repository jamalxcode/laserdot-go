package core

import (
	"bytes"
	"path/filepath"
	"slices"
	"testing"
)

// The owner: "i need to choose the drives and possibly directories it
// searches, have it as a choice also". Results on this Mac and on a drive
// offer This Mac and the drive; results in several folders offer those
// folders. Each count is what choosing it gives, and after choosing, the
// others stay to switch to.
func TestResultsOfferTheDrivesAndFoldersTheyAreIn(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Archive")
	home := filepath.Join(m.base, "home")
	// All of this Mac's invoices are under one folder (Work): the choices
	// are the folders inside it.
	writeAged(t, filepath.Join(home, "Work/2024/invoice-a.pdf"), bytes.Repeat([]byte("a"), 50))
	writeAged(t, filepath.Join(home, "Work/2024/invoice-b.pdf"), bytes.Repeat([]byte("b"), 50))
	writeAged(t, filepath.Join(home, "Work/2025/invoice-c.pdf"), bytes.Repeat([]byte("c"), 50))
	writeAged(t, filepath.Join(home, "Music/song.mp3"), []byte("x"))
	if _, err := m.ld.Index(home); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ld.CreateDriveIndex(drive, DriveOptions{}); err != nil {
		t.Fatal(err)
	}
	count := func(q string) uint64 {
		t.Helper()
		r, err := m.ld.Search(q, DefaultSearchOptions)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return r.Total
	}
	groups, err := m.ld.Refinements("invoice")
	if err != nil {
		t.Fatal(err)
	}
	drives := refineGroup(groups, "drive")
	if !slices.Contains(tokens(drives), "volume:local") || !slices.Contains(tokens(drives), "volume:archive") {
		t.Fatalf("This Mac and the drive are offered: %v (groups %+v)", tokens(drives), groups)
	}
	folders := refineGroup(groups, "folder")
	var labels []string
	for _, o := range folders {
		labels = append(labels, o.Label)
	}
	slices.Sort(labels)
	if !slices.Equal(labels, []string{"2024", "2025"}) {
		t.Fatalf("the folders inside Work are offered, got %v (%v)", labels, tokens(folders))
	}
	for _, o := range append(drives, folders...) {
		if got := count("invoice " + o.Token); got != o.Count || o.Count == 0 {
			t.Errorf("%s: offered with %d, the search finds %d", o.Token, o.Count, got)
		}
	}
	// Choosing a folder keeps the other folder and the drives to choose.
	chosen := folders[0].Token
	g2, err := m.ld.Refinements("invoice " + chosen)
	if err != nil {
		t.Fatal(err)
	}
	f2 := refineGroup(g2, "folder")
	if len(f2) != 2 {
		t.Fatalf("both folders stay: %+v", f2)
	}
	for _, o := range f2 {
		if o.Selected != (o.Token == chosen) {
			t.Errorf("%s selected=%v", o.Token, o.Selected)
		}
	}
	for _, o := range refineGroup(g2, "drive") {
		if got := count("invoice " + chosen + " " + o.Token); got != o.Count {
			t.Errorf("%s within the folder: offered with %d, the search finds %d", o.Token, o.Count, got)
		}
	}
	// Without a drive attached there is nothing to choose between.
	ld, root := newCore(t)
	writeAged(t, filepath.Join(root, "a/sample1.txt"), []byte("1"))
	writeAged(t, filepath.Join(root, "b/sample2.txt"), []byte("2"))
	// A folder that matches too does not stop the choice at it.
	writeAged(t, filepath.Join(root, "trips/2023/rome trip.txt"), []byte("r"))
	writeAged(t, filepath.Join(root, "trips/2024/paris trip.txt"), []byte("p"))
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	g3, _ := ld.Refinements("sample")
	if refineGroup(g3, "drive") != nil {
		t.Errorf("no drive group with no drive: %+v", g3)
	}
	if len(refineGroup(g3, "folder")) != 2 {
		t.Errorf("folders a and b: %+v", refineGroup(g3, "folder"))
	}
	g4, _ := ld.Refinements("trip")
	var l4 []string
	for _, o := range refineGroup(g4, "folder") {
		l4 = append(l4, o.Label)
	}
	slices.Sort(l4)
	if !slices.Equal(l4, []string{"2023", "2024"}) {
		t.Errorf("inside trips: %v", l4)
	}
}
