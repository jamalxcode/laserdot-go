package core

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jamalxcode/laserdot-go/internal/index"
)

func makeDrive(t *testing.T, vols, name string) string {
	t.Helper()
	d := filepath.Join(vols, name)
	writeAged(t, filepath.Join(d, "Films/trip.mp4"), corpus(t, "hevc10_pq.mp4"))
	writeAged(t, filepath.Join(d, "Films/concert.mkv"), corpus(t, "multi.mkv"))
	writeAged(t, filepath.Join(d, "Docs/invoice-march.pdf"), bytes.Repeat([]byte("p"), 300))
	writeAged(t, filepath.Join(d, "Docs/notes.txt"), bytes.Repeat([]byte("n"), 10))
	for i := 0; i < 20; i++ {
		writeAged(t, filepath.Join(d, fmt.Sprintf("Docs/batch/file-%02d.txt", i)), bytes.Repeat([]byte("x"), i*7))
	}
	d, _ = filepath.EvalSymlinks(d)
	return d
}

type mac struct {
	base, vols string
	ld         *Laserdot
}

func newMac(t *testing.T) *mac {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	vols := filepath.Join(base, "Volumes")
	os.MkdirAll(vols, 0o755)
	return &mac{base, vols, macAt(t, base, vols, "data")}
}

func macAt(t *testing.T, base, vols, name string) *Laserdot {
	t.Helper()
	ld, err := Open(filepath.Join(base, name, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ld.Close() })
	ld.SetVolumeConfig(VolumeConfig{Dir: vols})
	return ld
}

func dnames(t *testing.T, ld *Laserdot, q string) []string {
	t.Helper()
	r, err := ld.Search(q, SearchOptions{Page: index.Page{Limit: 1000}})
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	out := []string{}
	for _, h := range r.Hits {
		out = append(out, h.Name)
	}
	slices.Sort(out)
	return out
}

func TestStory44IndexLivesOnTheDriveAndIsSearchable(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Archive")
	writeAged(t, filepath.Join(m.base, "home/invoice-april.pdf"), []byte("pdf"))
	m.ld.Index(filepath.Join(m.base, "home"))

	plan, err := m.ld.PlanDrive(drive)
	if err != nil || plan.Files != 24 || !plan.OnDrive || plan.IndexEstimateBytes == 0 {
		t.Fatalf("%+v %v", plan, err)
	}
	if _, err := os.Stat(filepath.Join(drive, ".laserdot")); err == nil {
		t.Error("planning writes nothing")
	}
	if m.ld.Drives()[0].State.State != DriveUnindexed {
		t.Error("state")
	}
	s, err := m.ld.CreateDriveIndex(drive, DriveOptions{})
	if err != nil || s.Files != 24 {
		t.Fatalf("%+v %v", s, err)
	}
	for _, f := range []string{"index.db", "volume.json"} {
		if _, err := os.Stat(filepath.Join(drive, ".laserdot", f)); err != nil {
			t.Error(err)
		}
	}
	expect(t, dnames(t, m.ld, "invoice"), "invoice-april.pdf", "invoice-march.pdf")
	r, _ := m.ld.Search("invoice march", DefaultSearchOptions)
	if r.Hits[0].Volume == nil || *r.Hits[0].Volume != "Archive" {
		t.Errorf("%+v", r.Hits[0])
	}
	expect(t, dnames(t, m.ld, "codec:hevc hdr:hdr10"), "trip.mp4")
	expect(t, dnames(t, m.ld, "invoice volume:archive"), "invoice-march.pdf")
	expect(t, dnames(t, m.ld, "invoice volume:local"), "invoice-april.pdf")
	expect(t, dnames(t, m.ld, "index.db"))
	expect(t, dnames(t, m.ld, fmt.Sprintf("in:%q ext:pdf", filepath.Join(drive, "Docs"))), "invoice-march.pdf")
	info := m.ld.Drives()[0]
	if info.State.State != DriveAttached || !info.Complete || !info.OnDrive || info.Entries < 24 || info.IndexBytes == 0 {
		t.Errorf("%+v", info)
	}
	other := makeDrive(t, m.vols, "Spare")
	if _, err := m.ld.Index(filepath.Join(other, "Docs")); err == nil || !strings.Contains(err.Error(), "keeps its own index") {
		t.Error(err)
	}
}

func TestStory44PortableAcrossMacsAndMountPoints(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Archive")
	m.ld.CreateDriveIndex(drive, DriveOptions{})
	m.ld.Close()
	vols2 := filepath.Join(m.base, "OtherMac/Volumes")
	os.MkdirAll(vols2, 0o755)
	os.Rename(drive, filepath.Join(vols2, "Archive"))
	other := macAt(t, m.base, vols2, "other-data")
	if attached, _ := other.SyncDrives(); len(attached) != 1 {
		t.Fatal("an indexed drive attaches by itself")
	}
	moved := filepath.Join(vols2, "Archive")
	r, _ := other.Search("invoice march", DefaultSearchOptions)
	if r.Hits[0].Path != filepath.Join(moved, "Docs/invoice-march.pdf") {
		t.Error("paths follow the new mount point", r.Hits[0].Path)
	}
	expect(t, dnames(t, other, "res:1080p kind:video"), "trip.mp4")
	s, err := other.RefreshDrive(moved)
	if err != nil || s.Added+s.Removed+s.Modified+s.Probed != 0 {
		t.Errorf("catching up reads nothing: %+v %v", s, err)
	}
}

func TestStory46InterruptedIndexResumesAndMatchesAFreshOne(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Archive")
	if _, err := m.ld.CreateDriveIndex(drive, DriveOptions{Bulk: index.BulkOptions{Chunk: 5, StopAfterChunks: 2}}); err == nil {
		t.Fatal("unplugged mid-index")
	}
	m.ld.Close()
	again := macAt(t, m.base, m.vols, "data")
	if st := again.Drives()[0].State.State; st != DriveDetached {
		t.Fatal(st)
	}
	if _, err := again.AttachDrive(drive, ""); err != nil {
		t.Fatal(err)
	}
	if !again.Drives()[0].Complete {
		t.Error("resumed to completion")
	}
	resumed := dnames(t, again, "re:.")
	hevc := dnames(t, again, "codec:hevc")
	again.DetachDrive(drive)
	os.RemoveAll(filepath.Join(drive, ".laserdot"))
	fresh := macAt(t, m.base, m.vols, "fresh")
	fresh.CreateDriveIndex(drive, DriveOptions{})
	expect(t, resumed, dnames(t, fresh, "re:.")...)
	expect(t, hevc, "concert.mkv", "trip.mp4")
}

func TestReadOnlyDrivesKeepTheirIndexOnThisMac(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Camera")
	if _, err := m.ld.CreateDriveIndex(drive, DriveOptions{ForceOffDrive: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(drive, ".laserdot")); err == nil {
		t.Error("nothing written to the drive")
	}
	info := m.ld.Drives()[0]
	if info.OnDrive || !strings.HasPrefix(*info.IndexPath, filepath.Join(m.base, "data/drives")) {
		t.Errorf("%+v", info)
	}
	expect(t, dnames(t, m.ld, "notes"), "notes.txt")
	m.ld.Close()
	again := macAt(t, m.base, m.vols, "data")
	if a, _ := again.SyncDrives(); len(a) != 1 {
		t.Error("found again by volume")
	}
}

func TestStory47EncryptedDriveIndex(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Private")
	if _, err := m.ld.CreateDriveIndex(drive, DriveOptions{Passphrase: "correct horse"}); err != nil {
		t.Fatal(err)
	}
	expect(t, dnames(t, m.ld, "invoice"), "invoice-march.pdf")
	head, _ := os.ReadFile(filepath.Join(drive, ".laserdot/index.db"))
	if bytes.HasPrefix(head, []byte("SQLite format 3\x00")) || bytes.Contains(head, []byte("invoice")) {
		t.Fatal("the file must be encrypted")
	}
	m.ld.Close()
	other := macAt(t, m.base, m.vols, "other")
	if a, _ := other.SyncDrives(); len(a) != 0 {
		t.Error("locked drives wait for the passphrase")
	}
	if st := other.Drives()[0].State.State; st != DriveLocked {
		t.Error(st)
	}
	var locked *DriveLockedError
	if _, err := other.AttachDrive(drive, "wrong"); !errors.As(err, &locked) {
		t.Errorf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(drive, ".laserdot/index.db")); err != nil {
		t.Error("a wrong passphrase never damages the index")
	}
	if _, err := other.AttachDrive(drive, "correct horse"); err != nil {
		t.Fatal(err)
	}
	expect(t, dnames(t, other, "invoice"), "invoice-march.pdf")
	// Changes made through the working copy are sealed back to the drive.
	writeAged(t, filepath.Join(drive, "Docs/secret-plan.txt"), []byte("x"))
	other.RefreshDrive(drive)
	other.Close()
	third := macAt(t, m.base, m.vols, "third")
	third.AttachDrive(drive, "correct horse")
	expect(t, dnames(t, third, "secret-plan"), "secret-plan.txt")
}

func TestHostileDriveIndexCannotEscapeTheDrive(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Found")
	m.ld.CreateDriveIndex(drive, DriveOptions{})
	m.ld.Close()
	dbPath := filepath.Join(drive, ".laserdot/index.db")
	edit := func(stmts ...string) {
		c, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		for _, s := range stmts {
			if _, err := c.Exec(s); err != nil {
				t.Fatal(s, err)
			}
		}
	}
	// Someone edits the drive's index to point outside the drive.
	edit(`INSERT INTO entries (root, parent, name, is_dir, is_symlink, dataless, size, mtime, ctime, inode, dev, probe, width, height)
		SELECT root, id, '../../../etc/passwd', 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0 FROM entries WHERE parent IS NULL`,
		`UPDATE entries SET name = 'evil/../../x' WHERE name = 'notes.txt'`)
	other := macAt(t, m.base, m.vols, "other")
	if _, err := other.AttachDrive(drive, ""); err != nil {
		t.Fatal(err)
	}
	r, _ := other.Search("re:.", SearchOptions{Page: index.Page{Limit: 1000}})
	for _, h := range r.Hits {
		if !within(h.Path, drive) {
			t.Errorf("escaped: %s", h.Path)
		}
	}
	expect(t, dnames(t, other, "passwd"))
	other.Close()
	// A trigger hidden in the index is refused outright.
	edit("CREATE TRIGGER sneaky AFTER INSERT ON entries BEGIN DELETE FROM entries; END;")
	third := macAt(t, m.base, m.vols, "third")
	if _, err := third.AttachDrive(drive, ""); err == nil || !strings.Contains(err.Error(), "trigger") {
		t.Errorf("%v", err)
	}
}

func TestUnmountedDrivesDetachAndSortingMergesAcrossIndexes(t *testing.T) {
	m := newMac(t)
	drive := makeDrive(t, m.vols, "Archive")
	for i, n := range []int{5, 50, 500} {
		writeAged(t, filepath.Join(m.base, fmt.Sprintf("home/local-%d.txt", i)), bytes.Repeat([]byte("l"), n))
	}
	m.ld.Index(filepath.Join(m.base, "home"))
	m.ld.CreateDriveIndex(drive, DriveOptions{})
	var sizes []uint64
	for o := 0; o < 3; o++ {
		r, _ := m.ld.Search("ext:txt", SearchOptions{Page: index.Page{Sort: index.BySize, Offset: o * 4, Limit: 4}})
		for _, h := range r.Hits {
			sizes = append(sizes, h.Size)
		}
	}
	if !slices.IsSortedFunc(sizes, func(a, b uint64) int { return int(b) - int(a) }) || sizes[0] != 500 || len(sizes) != 12 {
		t.Errorf("one ordering across this Mac and the drive: %v", sizes)
	}
	writeAged(t, filepath.Join(drive, "Docs/new-on-drive.txt"), []byte("x"))
	m.ld.ApplyChanges([]Change{{Path: filepath.Join(drive, "Docs")}})
	expect(t, dnames(t, m.ld, "new-on-drive volume:archive"), "new-on-drive.txt")
	os.Rename(drive, filepath.Join(m.base, "unplugged"))
	if _, gone := m.ld.SyncDrives(); !slices.Equal(gone, []string{drive}) {
		t.Error(gone)
	}
	expect(t, dnames(t, m.ld, "invoice-march"))
	if len(dnames(t, m.ld, "local-1")) != 1 {
		t.Error("this Mac's results stay")
	}
}
