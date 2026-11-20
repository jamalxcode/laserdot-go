package core

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// slowDrive makes every folder under drive take d to read.
func slowDrive(t *testing.T, drive string, d time.Duration) {
	t.Helper()
	scan.SlowFoldersForTests(func(dir string) {
		if strings.HasPrefix(dir, drive) {
			time.Sleep(d)
		}
	})
	t.Cleanup(func() { scan.SlowFoldersForTests(nil) })
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A big drive is searchable from its first folders on, not after hours:
// results appear while it is still being indexed, and searching never waits
// for it.
func TestABigDriveIsSearchableWhileItIsIndexed(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Big")
	slowDrive(t, drive, 150*time.Millisecond)
	done := make(chan error, 1)
	go func() { _, err := m.ld.CreateDriveIndex(drive, DriveOptions{}); done <- err }()
	waitFor(t, "the first results", func() bool { return len(dnames(t, m.ld, "invoice")) > 0 })
	if info := m.ld.Drives()[0]; info.Complete || info.State.State != DriveAttached {
		t.Fatalf("expected an attached drive still being indexed: %+v", info)
	}
	start := time.Now()
	m.ld.Search("re:.", DefaultSearchOptions)
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Errorf("a search waited %v for the drive", d)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !m.ld.Drives()[0].Complete {
		t.Error("complete once done")
	}
	expect(t, dnames(t, m.ld, "codec:hevc"), "concert.mkv", "trip.mp4")
	expect(t, dnames(t, m.ld, "file-1"), "file-10.txt", "file-11.txt", "file-12.txt", "file-13.txt", "file-14.txt",
		"file-15.txt", "file-16.txt", "file-17.txt", "file-18.txt", "file-19.txt")
}

// Ejecting (or detaching, or quitting) in the middle of a first index stops
// it at once, without waiting for a slow folder; what was saved stays, and
// the next attach finishes the job: nothing missing, nothing twice.
func TestDetachingMidIndexStopsAtOnceAndResumes(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Archive")
	slowDrive(t, drive, 400*time.Millisecond)
	done := make(chan error, 1)
	go func() { _, err := m.ld.CreateDriveIndex(drive, DriveOptions{}); done <- err }()
	waitFor(t, "indexing to start", func() bool {
		d := m.ld.Drives()
		return len(d) > 0 && d[0].Entries > 0
	})
	start := time.Now()
	if !m.ld.DetachDrive(drive) {
		t.Fatal("detached")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("detaching waited %v", d)
	}
	if err := <-done; !errors.Is(err, index.ErrInterrupted) {
		t.Fatalf("the index should report it was stopped, got %v", err)
	}
	scan.SlowFoldersForTests(nil)
	if _, err := m.ld.AttachDrive(drive, ""); err != nil {
		t.Fatal(err)
	}
	m.ld.streams.Wait()
	if !m.ld.Drives()[0].Complete {
		t.Fatal("resumed to completion")
	}
	resumed := dnames(t, m.ld, "re:.")
	m.ld.DetachDrive(drive)
	os.RemoveAll(filepath.Join(drive, ".laserdot"))
	fresh := macAt(t, m.base, m.vols, "fresh")
	if _, err := fresh.CreateDriveIndex(drive, DriveOptions{}); err != nil {
		t.Fatal(err)
	}
	expect(t, resumed, dnames(t, fresh, "re:.")...)
}

// A returning APFS or HFS+ drive is caught up by comparing its folders'
// times (new, renamed and deleted files found without listing every
// folder); FAT, exFAT and NTFS drives, and any drive once a week, are read
// whole, which also finds a file changed in place.
func TestAReturningDriveIsCaughtUpByItsFolderTimes(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Photos")
	hourAgo := time.Now().Add(-time.Hour)
	filepath.WalkDir(drive, func(p string, d os.DirEntry, _ error) error {
		if d != nil && d.IsDir() {
			os.Chtimes(p, hourAgo, hourAgo)
		}
		return nil
	})
	if _, err := m.ld.CreateDriveIndex(drive, DriveOptions{}); err != nil {
		t.Fatal(err)
	}
	old := trustsFolderTimes
	defer func() { trustsFolderTimes = old }()
	trustsFolderTimes = func(string) bool { return true }

	write(t, filepath.Join(drive, "Docs"), "new-scan.pdf", "new")
	os.Rename(filepath.Join(drive, "Docs/notes.txt"), filepath.Join(drive, "Docs/memo.txt"))
	os.Remove(filepath.Join(drive, "Docs/invoice-march.pdf"))
	// Changed in place: its folder's time stays the same.
	batch := filepath.Join(drive, "Docs/batch")
	os.WriteFile(filepath.Join(batch, "file-03.txt"), []byte("much longer than before"), 0o644)
	os.Chtimes(batch, hourAgo, hourAgo)

	u, err := m.ld.CatchUpDrive(drive)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, dnames(t, m.ld, "new-scan"), "new-scan.pdf")
	expect(t, dnames(t, m.ld, "memo"), "memo.txt")
	expect(t, dnames(t, m.ld, "invoice"))
	if u.Modified != 0 {
		t.Errorf("the quick check lists only changed folders, yet found %d changed files", u.Modified)
	}

	trustsFolderTimes = func(string) bool { return false } // exFAT, say
	u, err = m.ld.CatchUpDrive(drive)
	if err != nil {
		t.Fatal(err)
	}
	if u.Modified != 1 {
		t.Errorf("a full read finds the file changed in place: %+v", u)
	}
}

// Searching in a folder of a drive still being indexed moves that folder up
// the queue (and the folders on the way to it).
func TestSearchDemandRaisesFolders(t *testing.T) {
	var d demand
	d.add([]string{"/Volumes/Big/Projects/2024"})
	switch {
	case d.priority("/Volumes/Big/Projects/2024/raw") <= d.priority("/Volumes/Big/Projects"):
		t.Error("inside the searched folder comes first")
	case d.priority("/Volumes/Big/Projects") <= d.priority("/Volumes/Big/Other"):
		t.Error("the way to it comes next")
	case d.priority("/Volumes/Big/Other") != 0:
		t.Error("the rest unchanged")
	}
	v := d.currentVersion()
	d.add([]string{"/Volumes/Big/Other"})
	if d.currentVersion() == v {
		t.Error("a new search reorders the queue")
	}
}

// Ranking a very broad search in stages (bounded memory) gives exactly the
// pages ranking everything at once gives, in every order.
func TestStagedRankingMatchesRankingAllAtOnce(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Wide")
	if _, err := m.ld.CreateDriveIndex(drive, DriveOptions{}); err != nil {
		t.Fatal(err)
	}
	page := func(q string, srt index.Sort, offset int) []string {
		r, err := m.ld.Search(q, SearchOptions{Page: index.Page{Sort: srt, Offset: offset, Limit: 5}})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range r.Hits {
			out = append(out, h.Path)
		}
		return out
	}
	old := index.RankStage
	defer func() { index.RankStage = old }()
	for _, q := range []string{"re:.", "ext:txt", "file"} {
		for _, srt := range []index.Sort{index.Relevance, index.ByName, index.ByKind, index.BySize, index.ByModified} {
			for _, off := range []int{0, 5, 20} {
				index.RankStage = 1 << 18
				want := page(q, srt, off)
				index.RankStage = 3
				if got := page(q, srt, off); !slices.Equal(got, want) {
					t.Errorf("%q by %v from %d: staged %v, at once %v", q, srt, off, got, want)
				}
			}
		}
	}
}

// A drive whose first index was cut short by a version before 0.9.19 (no
// folder checkpoints, half its files saved) starts over with the streaming
// indexer in the background, instead of a whole-drive pass held in memory,
// and ends equal to a fresh index.
func TestAnOldCutShortDriveIndexStartsOverByStreaming(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Old")
	if _, err := m.ld.CreateDriveIndex(drive, DriveOptions{}); err != nil {
		t.Fatal(err)
	}
	full := dnames(t, m.ld, "re:.")
	m.ld.DetachDrive(drive)
	// What 0.9.18 left: incomplete, part of the files, no marks.
	c, err := sql.Open("sqlite", filepath.Join(drive, ".laserdot/index.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"UPDATE roots SET complete = 0", "DELETE FROM entries WHERE name LIKE 'file-1%'"} {
		if _, err := c.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	c.Close()
	if _, err := m.ld.AttachDrive(drive, ""); err != nil {
		t.Fatal(err)
	}
	m.ld.streams.Wait()
	if !m.ld.Drives()[0].Complete {
		t.Fatal("finished")
	}
	expect(t, dnames(t, m.ld, "re:."), full...)
}

// filesOpenUnder lists the files this process has open below dir (Linux:
// /proc/self/fd; elsewhere the test is skipped).
func filesOpenUnder(t *testing.T, dir string) []string {
	t.Helper()
	fds, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skip("no /proc here")
	}
	var open []string
	for _, fd := range fds {
		if p, err := os.Readlink(filepath.Join("/proc/self/fd", fd.Name())); err == nil && strings.HasPrefix(p, dir+"/") {
			open = append(open, p)
		}
	}
	return open
}

// Between jobs Laserdot holds nothing open on a drive, so macOS can eject it
// (the owner could not eject a drive while Laserdot ran): its index is
// opened for a job and closed after, and searching uses memory only.
func TestAnIdleDriveHoldsNoOpenFiles(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Archive")
	if _, err := m.ld.CreateDriveIndex(drive, DriveOptions{}); err != nil {
		t.Fatal(err)
	}
	if open := filesOpenUnder(t, drive); len(open) > 0 {
		t.Fatalf("after indexing, still open on the drive: %v", open)
	}
	expect(t, dnames(t, m.ld, "invoice"), "invoice-march.pdf") // searching needs no file
	if _, err := m.ld.CatchUpDrive(drive); err != nil {
		t.Fatal(err)
	}
	m.ld.Drives()
	if open := filesOpenUnder(t, drive); len(open) > 0 {
		t.Fatalf("after catching up and listing drives, still open: %v", open)
	}
	if !m.ld.Drives()[0].Complete {
		t.Error("the drive list still knows the index is complete")
	}
	// Reattached later (as when it is plugged in again): nothing held either.
	m.ld.DetachDrive(drive)
	if _, err := m.ld.AttachDrive(drive, ""); err != nil {
		t.Fatal(err)
	}
	m.ld.streams.Wait()
	if open := filesOpenUnder(t, drive); len(open) > 0 {
		t.Fatalf("after attaching, still open: %v", open)
	}
}

// Ejecting during a whole-drive catch-up (exFAT, or the weekly check) lets
// go at the next folder instead of after the whole drive.
func TestEjectingDuringACatchUpLetsGoAtOnce(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Stick")
	if _, err := m.ld.CreateDriveIndex(drive, DriveOptions{}); err != nil {
		t.Fatal(err)
	}
	old := trustsFolderTimes
	defer func() { trustsFolderTimes = old }()
	trustsFolderTimes = func(string) bool { return false }
	slowDrive(t, drive, 300*time.Millisecond)
	done := make(chan error, 1)
	go func() { _, err := m.ld.CatchUpDrive(drive); done <- err }()
	time.Sleep(400 * time.Millisecond) // reading the drive now
	start := time.Now()
	if !m.ld.DetachDrive(drive) {
		t.Fatal("detached")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("detaching waited %v for the catch-up", d)
	}
	if err := <-done; err == nil {
		t.Error("the catch-up should report it was stopped")
	}
	if open := filesOpenUnder(t, drive); len(open) > 0 {
		t.Fatalf("still open after detaching: %v", open)
	}
}

// Stop keeps the drive attached and searchable with what was read; Continue
// goes on from there and finishes; the hourly catch-up does not restart a
// stopped index by itself.
func TestStopAndContinueADriveIndex(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Movies")
	slowDrive(t, drive, 150*time.Millisecond)
	done := make(chan error, 1)
	go func() { _, err := m.ld.CreateDriveIndex(drive, DriveOptions{}); done <- err }()
	waitFor(t, "indexing to start", func() bool {
		d := m.ld.Drives()
		return len(d) > 0 && d[0].Entries > 0 && d[0].Indexing
	})
	if !m.ld.StopDriveIndex(drive) {
		t.Fatal("a running index can be stopped")
	}
	if err := <-done; err != nil {
		t.Fatalf("stopping is not an error: %v", err)
	}
	info := m.ld.Drives()[0]
	if info.State.State != DriveAttached || info.Indexing || info.Complete {
		t.Fatalf("stopped: attached, not indexing, not complete; got %+v", info)
	}
	if len(dnames(t, m.ld, "re:.")) == 0 {
		t.Error("what was read stays searchable")
	}
	m.ld.CatchUpDrive(drive)
	if m.ld.Drives()[0].Indexing || m.ld.Drives()[0].Complete {
		t.Error("the catch-up does not continue a stopped index")
	}
	scan.SlowFoldersForTests(nil)
	if !m.ld.ContinueDriveIndex(drive) {
		t.Fatal("continue")
	}
	m.ld.streams.Wait()
	if !m.ld.Drives()[0].Complete {
		t.Error("continued to completion")
	}
	expect(t, dnames(t, m.ld, "codec:hevc"), "concert.mkv", "trip.mp4")
}

// A duplicate scan does not hold a drive being ejected: its files are not
// read once the drive is let go.
func TestDuplicatesLetGoOfADriveBeingEjected(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Backup")
	writeAged(t, filepath.Join(drive, "Copies/a.bin"), []byte(strings.Repeat("same", 5000)))
	writeAged(t, filepath.Join(drive, "Copies/b.bin"), []byte(strings.Repeat("same", 5000)))
	if _, err := m.ld.CreateDriveIndex(drive, DriveOptions{}); err != nil {
		t.Fatal(err)
	}
	opts := DefaultDupeOptions()
	opts.Images, opts.Videos = false, false
	before, err := m.ld.FindDuplicates(opts)
	if err != nil || before.BytesRead == 0 {
		t.Fatalf("the drive's copies are read normally: %+v %v", before, err)
	}
	m.ld.release(drive) // what detaching and ejecting do first
	after, err := m.ld.FindDuplicates(opts)
	if err != nil {
		t.Fatal(err)
	}
	if after.BytesRead != 0 {
		t.Errorf("read %d bytes on a drive being ejected", after.BytesRead)
	}
}
