package core

import (
	"bytes"
	"path/filepath"
	"slices"
	"testing"
	"time"
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
	// Large to granular (the owner): where first, then what the results are
	// (folders and documents here), then size; each group with its family.
	var ids []string
	for _, g := range g4 {
		ids = append(ids, g.ID)
	}
	if !slices.Equal(ids, []string{"folder", "kind", "type", "size"}) {
		t.Errorf("groups top down: %v", ids)
	}
	if k := refineGroup(g4, "kind"); !slices.Contains(tokens(k), "kind:folder") || !slices.Contains(tokens(k), "kind:document") {
		t.Errorf("kinds: %v", tokens(k))
	}
	for _, g := range g4 {
		want := map[string]string{"folder": "where", "kind": "what", "type": "what", "size": "file"}[g.ID]
		if g.Family != want {
			t.Errorf("%s: family %q, want %q", g.ID, g.Family, want)
		}
	}
}

// The owner: "not all the drives that are mounted appear … especially the
// larger drive that i wanted to look into" (four drives mounted, only the
// indexed one offered). A mounted drive with no index shows among the
// drives too, marked so the app offers to index it — also when every result
// is on this Mac.
func TestMountedDrivesWithNoIndexAreOffered(t *testing.T) {
	m := newMac(t)
	indexed := makeDrive(t, m.vols, "Rapid")
	plex := makeDrive(t, m.vols, "plex media")
	home := filepath.Join(m.base, "home")
	writeAged(t, filepath.Join(home, "a/movie one.mkv"), []byte("x"))
	writeAged(t, filepath.Join(home, "b/movie two.mkv"), []byte("y"))
	if _, err := m.ld.Index(home); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ld.CreateDriveIndex(indexed, DriveOptions{}); err != nil {
		t.Fatal(err)
	}
	m.ld.SyncDrives() // what the daemon does every few seconds
	groups, err := m.ld.Refinements("movie")
	if err != nil {
		t.Fatal(err)
	}
	var waiting *RefineOption
	for _, o := range refineGroup(groups, "drive") {
		if o.Mount == plex {
			waiting = &o
		}
	}
	if waiting == nil || waiting.State != DriveUnindexed || waiting.Label != "plex media" || waiting.Count != 0 {
		t.Fatalf("the drive with no index is offered to index: %+v (%v)", waiting, tokens(refineGroup(groups, "drive")))
	}
	if !slices.Contains(tokens(refineGroup(groups, "drive")), "volume:local") {
		t.Error("This Mac shows beside it")
	}
	// Once indexed, it is an ordinary choice.
	if _, err := m.ld.CreateDriveIndex(plex, DriveOptions{}); err != nil {
		t.Fatal(err)
	}
	m.ld.SyncDrives()
	groups, _ = m.ld.Refinements("movie")
	for _, o := range refineGroup(groups, "drive") {
		if o.State != "" {
			t.Errorf("no drive left to index: %+v", o)
		}
	}
}

// The owner, shown "Index 5A742321-B17B-…?" for 4 files: "I have no idea
// what drive this is". macOS's own mounts (hidden: "don't browse", a dot
// name, Recovery) are neither listed nor offered — unless indexed.
func TestHiddenSystemMountsAreNotOffered(t *testing.T) {
	m := newMac(t)
	makeDrive(t, m.vols, "Daisy Backup")
	hidden := makeDrive(t, m.vols, ".timemachine")
	var names []string
	for _, d := range m.ld.Drives() {
		names = append(names, d.Name)
	}
	if !slices.Equal(names, []string{"Daisy Backup"}) {
		t.Fatalf("only the real drive is listed: %v", names)
	}
	// Once it has an index (made by hand), it is listed like any drive.
	if _, err := m.ld.CreateDriveIndex(hidden, DriveOptions{}); err != nil {
		t.Fatal(err)
	}
	names = nil
	for _, d := range m.ld.Drives() {
		names = append(names, d.Name)
	}
	if len(names) != 2 {
		t.Errorf("an indexed hidden drive stays listed: %v", names)
	}
}

// The owner, after 0.9.52: "now drives are not showing and there is a
// searching… on bottom left that's been there for over a minute". Listing
// the drives took the index lock for writing; Go then makes every new
// reader (every search) wait behind it while a long reader runs (a big
// drive being read). The list must never hold searches up.
func TestListingDrivesNeverHoldsSearchesUp(t *testing.T) {
	m := newMac(t)
	makeDrive(t, m.vols, "plex media")
	home := filepath.Join(m.base, "home")
	writeAged(t, filepath.Join(home, "movie.mkv"), []byte("x"))
	if _, err := m.ld.Index(home); err != nil {
		t.Fatal(err)
	}
	// A long reader: something reading the index for a while.
	m.ld.mu.RLock()
	released := make(chan struct{})
	go func() {
		time.Sleep(2 * time.Second)
		m.ld.mu.RUnlock()
		close(released)
	}()
	go m.ld.Drives() // the app asks every few seconds; so does the daemon
	time.Sleep(100 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		m.ld.Search("movie", DefaultSearchOptions)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("a search waited behind the drive list")
	}
	<-released
}
