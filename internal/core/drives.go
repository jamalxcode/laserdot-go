package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// Portable per-drive indexes (spec §9). A drive's index lives on the drive
// (<mount>/.laserdot/index.db, paths relative to the mount) or, for a
// read-only drive, on this Mac under drives/<volume key>/. It is untrusted
// input: opened through index.OpenDrive.

// VolumeConfig says where drives mount.
type VolumeConfig struct {
	Dir string
	// MountsOnly: only real mount points count (tests use plain folders).
	MountsOnly bool
}

// DefaultVolumeConfig: /Volumes on macOS, /media elsewhere.
func DefaultVolumeConfig() VolumeConfig {
	if runtime.GOOS == "darwin" {
		return VolumeConfig{Dir: "/Volumes", MountsOnly: true}
	}
	return VolumeConfig{Dir: "/media", MountsOnly: true}
}

// Drive states.
const (
	DriveUnindexed  = "unindexed"
	DriveAttached   = "attached"
	DriveDetached   = "detached"
	DriveLocked     = "locked"
	DriveUnreadable = "unreadable"
)

// DriveState is a drive's index state; Detail explains "unreadable".
// JSON: {"state":"attached"} or {"state":"unreadable","detail":"…"}.
type DriveState struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// DriveInfo describes a mounted drive.
type DriveInfo struct {
	ID         *string    `json:"id"`
	Name       string     `json:"name"`
	Mount      string     `json:"mount"`
	FSType     string     `json:"fs_type"`
	ReadOnly   bool       `json:"read_only"`
	TotalBytes uint64     `json:"total_bytes"`
	FreeBytes  uint64     `json:"free_bytes"`
	State      DriveState `json:"state"`
	OnDrive    bool       `json:"on_drive"`
	IndexPath  *string    `json:"index_path"`
	IndexBytes uint64     `json:"index_bytes"`
	Encrypted  bool       `json:"encrypted"`
	Entries    uint64     `json:"entries"`
	Complete   bool       `json:"complete"`
	// Indexing: a first index is running now (it can be stopped and
	// continued: StopDriveIndex, ContinueDriveIndex).
	Indexing bool   `json:"indexing"`
	LastScan *int64 `json:"last_scan"`
}

// DrivePlan estimates a drive's index before writing anything (§9.3).
type DrivePlan struct {
	Name               string `json:"name"`
	Mount              string `json:"mount"`
	Files              uint64 `json:"files"`
	Folders            uint64 `json:"folders"`
	Bytes              uint64 `json:"bytes"`
	IndexEstimateBytes uint64 `json:"index_estimate_bytes"`
	Location           string `json:"location"`
	OnDrive            bool   `json:"on_drive"`
	// Partial: counting stopped after PlanBudget; Files and Folders are at
	// least these, Bytes is the space used on the drive.
	Partial bool `json:"partial,omitempty"`
}

// PlanBudget: how long the drive prompt's count may take.
var PlanBudget = 5 * time.Second

// DriveOptions for creating a drive index.
type DriveOptions struct {
	// Passphrase encrypts the index (§9.8); "" = not encrypted.
	Passphrase string
	// ForceOffDrive keeps the index on this Mac even if the drive is
	// writable.
	ForceOffDrive bool
	// Stream tunes the streaming index (tests: small batches, a simulated
	// unplug).
	Stream index.StreamOptions
}

// DriveLockedError: the drive's index is encrypted and the passphrase is
// missing or wrong.
type DriveLockedError struct{ Drive string }

func (e *DriveLockedError) Error() string {
	return fmt.Sprintf("the index on %q is encrypted: enter its passphrase", e.Drive)
}

// Code is the protocol's machine-readable error kind.
func (e *DriveLockedError) Code() string { return "drive_locked" }

// volumeFile is .laserdot/volume.json.
type volumeFile struct {
	Format    int    `json:"format"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Created   int64  `json:"created"`
	Encrypted bool   `json:"encrypted"`
}

type drive struct {
	id, mount, name, dir string
	onDrive, encrypted   bool
	pass                 string
	// db: the drive's index while a job uses it. An index kept on the drive
	// is opened for each job (index, catch up) and closed after it, so
	// between jobs Laserdot holds no file on the drive and it can be ejected
	// (the owner could not eject a drive Laserdot kept its index open on).
	// Searching uses the in-memory store and never needs it. Encrypted and
	// read-only drives' indexes are worked on in a copy on this Mac, which
	// stays open. Use under wmu (or before the drive is shared).
	db *index.Db
	// roots: the index's roots as last read (for the drive list, which must
	// not open the drive).
	roots atomic.Pointer[[]index.RootInfo]
	// work: the decrypted working copy on this Mac (encrypted drives).
	work  string
	store *index.Store
	scan  scan.Options
	// wmu: one writer at a time for this drive's index. The writer looks at
	// the drive (minutes, on a big one) holding only this; searches go on.
	wmu sync.Mutex
	// closed: detached; a writer that waited for wmu stops.
	closed bool
	// stop: asked to let go (detach, eject, quit). A first index running
	// for hours checks it between batches and stops within moments,
	// keeping what it saved.
	stop atomic.Bool
	// indexing: a first index (or its resumption) is running.
	indexing atomic.Bool
	// paused: the user stopped the first index (Stop); the drive stays
	// attached and searchable with what was read, until Continue.
	paused atomic.Bool
}

func (d *drive) indexPath() string { return filepath.Join(d.dir, "index.db") }

// holdsDrive: the open index is a file on the drive itself.
func (d *drive) holdsDrive() bool { return d.onDrive && !d.encrypted }

// acquire opens the drive's index for a job, if it is closed.
func (d *drive) acquire() error {
	if d.db != nil {
		return nil
	}
	db, err := index.OpenDrive(d.indexPath(), d.mount)
	if err != nil {
		return err
	}
	d.db = db
	return nil
}

// release notes the roots and, for an index on the drive, closes it (its
// journal files go too), so the drive is free to be ejected.
func (d *drive) release() {
	if d.db == nil {
		return
	}
	if roots, err := d.db.Roots(); err == nil {
		d.roots.Store(&roots)
	}
	if d.holdsDrive() {
		d.db.Close()
		d.db = nil
	}
}

// complete: every root of the drive's index is finished (as last read).
func (d *drive) complete() bool {
	r := d.roots.Load()
	return r != nil && !slices.ContainsFunc(*r, func(x index.RootInfo) bool { return !x.Complete })
}

func (d *drive) reload() error {
	s, err := d.load()
	if err != nil {
		return err
	}
	d.store = s
	return nil
}

// load builds the drive's in-memory index from its database (slow for a big
// drive: done without locks; the caller swaps it in).
func (d *drive) load() (*index.Store, error) {
	raw, err := d.db.Load()
	if err != nil {
		return nil, err
	}
	s := index.Build(raw)
	s.SetVolumeName(d.name)
	return s, nil
}

// sealBack writes an encrypted drive's working copy back to the drive.
func (d *drive) sealBack() error {
	if !d.encrypted || d.db == nil {
		return nil
	}
	if err := d.db.Checkpoint(); err != nil {
		return err
	}
	return index.Seal(d.work, d.indexPath(), d.pass)
}

func (d *drive) close() {
	if d.db != nil {
		d.sealBack()
		d.db.Close()
		d.db = nil
	}
	if d.work != "" {
		for _, s := range []string{"", "-wal", "-shm"} {
			os.Remove(d.work + s)
		}
	}
}

// indexBytesPerEntry estimates the index size per entry.
const indexBytesPerEntry = 220

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// volumeKey identifies a volume for an index kept on this Mac.
func volumeKey(v scan.Volume) string {
	h := uint64(0xcbf29ce484222325)
	for _, b := range []byte(fmt.Sprintf("%s|%s|%d", v.Name, v.FSType, v.TotalBytes)) {
		h = (h ^ uint64(b)) * 0x100000001b3
	}
	return fmt.Sprintf("%016x", h)
}

func dirBytes(dir string) uint64 {
	var n uint64
	des, _ := os.ReadDir(dir)
	for _, de := range des {
		if fi, err := de.Info(); err == nil {
			n += uint64(fi.Size())
		}
	}
	return n
}

func (ld *Laserdot) drivesDataDir() string { return filepath.Join(filepath.Dir(ld.dbPath), "drives") }

func (ld *Laserdot) offDriveDir(v scan.Volume) string {
	return filepath.Join(ld.drivesDataDir(), volumeKey(v))
}

// findIndex finds an existing index for v: on the drive, then on this Mac.
func (ld *Laserdot) findIndex(v scan.Volume) (dir string, onDrive bool, vf volumeFile, ok bool) {
	for _, c := range []struct {
		dir string
		on  bool
	}{{filepath.Join(v.Mount, ".laserdot"), true}, {ld.offDriveDir(v), false}} {
		b, err := os.ReadFile(filepath.Join(c.dir, "volume.json"))
		if err != nil || json.Unmarshal(b, &vf) != nil || vf.ID == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(c.dir, "index.db")); err == nil {
			return c.dir, c.on, vf, true
		}
	}
	return "", false, volumeFile{}, false
}

func driveScanOptions(v scan.Volume) scan.Options {
	o := scan.DefaultOptions()
	o.StableInodes = v.StableInodes
	// The drive's own index is never indexed.
	o.ExcludedPaths = append(o.ExcludedPaths, filepath.Join(v.Mount, ".laserdot"))
	return o
}

// driveAt is the attached drive at mount; caller holds mu.
func (ld *Laserdot) driveAt(mount string) *drive {
	for _, d := range ld.drives {
		if d.mount == mount {
			return d
		}
	}
	return nil
}

// driveFor is the attached drive holding path; caller holds mu.
func (ld *Laserdot) driveFor(path string) *drive {
	for _, d := range ld.drives {
		if within(path, d.mount) {
			return d
		}
	}
	return nil
}
