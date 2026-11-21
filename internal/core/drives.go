package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/crash"
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

// SetVolumeConfig sets where drives mount.
func (ld *Laserdot) SetVolumeConfig(c VolumeConfig) {
	ld.mu.Lock()
	ld.volumes = c
	ld.mu.Unlock()
}

// Drives lists mounted drives and their index state.
func (ld *Laserdot) Drives() []DriveInfo {
	// Every look at the disks happens without the lock: a network share that
	// stops answering can hang a stat for a long time, and holding the lock
	// meanwhile stalled indexing and then every search behind it.
	ld.mu.RLock()
	vc := ld.volumes
	attached := map[string]string{} // mount → its index folder
	for _, d := range ld.drives {
		attached[d.mount] = d.dir
	}
	ld.mu.RUnlock()
	vols := scan.ListVolumes(vc.Dir, vc.MountsOnly)
	for m := range attached {
		if !slices.ContainsFunc(vols, func(v scan.Volume) bool { return v.Mount == m }) {
			if v, err := scan.VolumeAt(m); err == nil {
				vols = append(vols, v)
			}
		}
	}
	slices.SortFunc(vols, func(a, b scan.Volume) int { return strings.Compare(a.Mount, b.Mount) })
	found := make([]foundIndex, len(vols))
	for i, v := range vols {
		if dir, ok := attached[v.Mount]; ok {
			found[i].bytes = dirBytes(dir)
			continue
		}
		f := &found[i]
		f.dir, f.on, f.vf, f.ok = ld.findIndex(v)
		if f.ok {
			f.bytes = dirBytes(f.dir)
		}
	}
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	out := []DriveInfo{}
	for i, v := range vols {
		out = append(out, ld.driveInfoFrom(v, found[i]))
	}
	return out
}

// foundIndex: what was read from a volume (outside the lock) for its info.
type foundIndex struct {
	dir   string
	on    bool
	vf    volumeFile
	ok    bool
	bytes uint64 // the index folder's size
}

// driveInfo reads the volume's index itself (callers that already hold mu
// for a drive just attached).
func (ld *Laserdot) driveInfo(v scan.Volume) DriveInfo {
	var f foundIndex
	if d := ld.driveAt(v.Mount); d != nil {
		f.bytes = dirBytes(d.dir)
	} else {
		f.dir, f.on, f.vf, f.ok = ld.findIndex(v)
		if f.ok {
			f.bytes = dirBytes(f.dir)
		}
	}
	return ld.driveInfoFrom(v, f)
}

// driveInfoFrom builds a volume's info from what was read; caller holds mu.
func (ld *Laserdot) driveInfoFrom(v scan.Volume, f foundIndex) DriveInfo {
	info := DriveInfo{
		Name: v.Name, Mount: v.Mount, FSType: v.FSType, ReadOnly: v.ReadOnly, TotalBytes: v.TotalBytes,
		FreeBytes: v.FreeBytes, State: DriveState{State: DriveUnindexed},
	}
	if d := ld.driveAt(v.Mount); d != nil {
		var roots []index.RootInfo
		if r := d.roots.Load(); r != nil {
			roots = *r
		}
		id, p := d.id, d.indexPath()
		info.ID, info.IndexPath = &id, &p
		info.State = DriveState{State: DriveAttached}
		info.OnDrive, info.Encrypted = d.onDrive, d.encrypted
		info.IndexBytes = f.bytes
		info.Entries = d.store.LiveCount()
		info.Indexing = d.indexing.Load()
		info.Complete = !info.Indexing
		for _, r := range roots {
			info.Complete = info.Complete && r.Complete
			if info.LastScan == nil || r.LastVerified > *info.LastScan {
				lv := r.LastVerified
				info.LastScan = &lv
			}
		}
	} else if f.ok {
		dir, on, vf := f.dir, f.on, f.vf
		p := filepath.Join(dir, "index.db")
		info.ID, info.IndexPath = &vf.ID, &p
		info.OnDrive, info.Encrypted = on, vf.Encrypted
		info.IndexBytes = f.bytes
		info.State = DriveState{State: DriveDetached}
		if _, have := ld.keys[vf.ID]; vf.Encrypted && !have {
			info.State = DriveState{State: DriveLocked}
		}
	}
	return info
}

// PlanDrive estimates a drive's index without writing anything.
func (ld *Laserdot) PlanDrive(mount string) (DrivePlan, error) {
	mount, err := canonical(mount)
	if err != nil {
		return DrivePlan{}, err
	}
	v, err := scan.VolumeAt(mount)
	if err != nil {
		return DrivePlan{}, err
	}
	// A few seconds of counting at most: an 8 TB drive would otherwise keep
	// the user waiting minutes for the question (indexing reads it anyway).
	files, dirs, bytes, partial, err := scan.EstimateWithin(mount, driveScanOptions(v), PlanBudget)
	if err != nil {
		return DrivePlan{}, err
	}
	// Estimate counts the mount itself as a folder.
	dirs = max(dirs, 1) - 1
	p := DrivePlan{Name: v.Name, Mount: mount, Files: files, Folders: dirs, Bytes: bytes,
		IndexEstimateBytes: (files + dirs) * indexBytesPerEntry, OnDrive: !v.ReadOnly, Partial: partial}
	if p.OnDrive {
		p.Location = filepath.Join(mount, ".laserdot")
	} else {
		ld.mu.RLock()
		p.Location = ld.offDriveDir(v)
		ld.mu.RUnlock()
	}
	return p, nil
}

// openDriveDB opens a drive's index (decrypting an encrypted one into a
// working copy on this Mac).
func (ld *Laserdot) openDriveDB(dir, mount, id, pass string, encrypted bool) (*index.Db, string, error) {
	path := filepath.Join(dir, "index.db")
	if !encrypted {
		db, err := index.OpenDrive(path, mount)
		return db, "", err
	}
	work := filepath.Join(ld.drivesDataDir(), "work-"+id+".db")
	for _, s := range []string{"", "-wal", "-shm"} {
		os.Remove(work + s)
	}
	if index.IsSealed(path) {
		if err := index.Unseal(path, work, pass); err != nil {
			return nil, "", err
		}
	}
	db, err := index.OpenDrive(work, mount)
	return db, work, err
}

// CreateDriveIndex indexes a drive into its own portable index.
func (ld *Laserdot) CreateDriveIndex(mount string, opts DriveOptions) (IndexStats, error) {
	mount, err := canonical(mount)
	if err != nil {
		return IndexStats{}, err
	}
	if err := enoughSpace(mount, ld.dbDir()); err != nil {
		return IndexStats{}, err
	}
	v, err := scan.VolumeAt(mount)
	if err != nil {
		return IndexStats{}, err
	}
	// No global lock while the drive is read (hours, for a big drive):
	// the mount is reserved instead, so it is not indexed or attached twice.
	ld.mu.Lock()
	_, _, _, exists := ld.findIndex(v)
	exists = exists || ld.driveAt(mount) != nil
	busy := ld.creating[mount]
	if !exists && !busy {
		ld.creating[mount] = true
	}
	ld.mu.Unlock()
	if busy {
		return IndexStats{}, fmt.Errorf("%s is being indexed already", mount)
	}
	if exists {
		return IndexStats{}, fmt.Errorf("%s already has an index — rebuild it instead", mount)
	}
	defer func() {
		ld.mu.Lock()
		delete(ld.creating, mount)
		ld.mu.Unlock()
	}()
	dir, onDrive := filepath.Join(mount, ".laserdot"), true
	if opts.ForceOffDrive || v.ReadOnly || os.MkdirAll(dir, 0o755) != nil {
		dir, onDrive = ld.offDriveDir(v), false
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return IndexStats{}, err
		}
	}
	ld.releasedMounts.Delete(mount)
	vf := volumeFile{Format: 1, ID: randomID(), Name: v.Name, Created: time.Now().Unix(), Encrypted: opts.Passphrase != ""}
	b, _ := json.MarshalIndent(vf, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "volume.json"), b, 0o644); err != nil {
		return IndexStats{}, err
	}
	os.MkdirAll(ld.drivesDataDir(), 0o700)
	db, work, err := ld.openDriveDB(dir, mount, vf.ID, opts.Passphrase, vf.Encrypted)
	if err != nil {
		return IndexStats{}, err
	}
	d := &drive{id: vf.ID, mount: mount, name: v.Name, dir: dir, onDrive: onDrive, encrypted: vf.Encrypted,
		pass: opts.Passphrase, db: db, work: work, scan: driveScanOptions(v)}
	if vf.Encrypted {
		ld.mu.Lock()
		ld.keys[vf.ID] = opts.Passphrase
		ld.mu.Unlock()
	}
	// Attached at once (empty until its first folders are saved), so
	// searches include it as it fills.
	d.store = index.Build(nil)
	d.store.SetVolumeName(d.name)
	ld.mu.Lock()
	ld.drives = append(ld.drives, d)
	ld.mu.Unlock()
	return ld.streamDrive(d, opts.Stream)
}

// streamDrive indexes a drive with the streaming indexer, or resumes an
// earlier run (index.IndexStreaming). The drive is attached throughout and
// its results appear as folders are saved: a big drive is searchable from
// its first seconds, shallow and recently changed folders first. Only the
// drive's writer lock is held; searches wait only for the moment each batch
// joins the in-memory index. Detaching, ejecting or quitting stops it
// within moments; unplugging stops it too. Either way what was saved stays,
// and the next attach goes on from there.
func (ld *Laserdot) streamDrive(d *drive, so index.StreamOptions) (IndexStats, error) {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if d.closed {
		return IndexStats{}, errors.New("drive not attached")
	}
	if err := d.acquire(); err != nil {
		return IndexStats{}, err
	}
	defer d.release()
	d.indexing.Store(true)
	defer d.indexing.Store(false)
	mount := d.mount
	if so.Progress == nil {
		so.Progress = func(saved uint64) {
			ld.progress.Store(&IndexProgress{Root: mount, Phase: "scanning", Done: saved})
		}
		defer ld.progress.Store(nil)
	}
	if so.Pause == nil {
		so.Pause = ld.paused.Load
	}
	userStop := so.Stop
	so.Stop = func() bool { return d.stop.Load() || d.paused.Load() || (userStop != nil && userStop()) }
	if so.Priority == nil {
		so.Priority = ld.searchPriority
		so.PriorityVersion = ld.demand.currentVersion
	}
	// folders: database id → in-memory id, so each saved row finds its
	// parent (folders only: files need no lookup).
	var folders map[int64]uint32
	so.OnStart = func() error {
		if roots, err := d.db.Roots(); err == nil {
			d.roots.Store(&roots)
		}
		s, err := d.load()
		if err != nil {
			return err
		}
		folders = map[int64]uint32{}
		it := s.Live().Iterator()
		for it.HasNext() {
			if id := it.Next(); s.IsDir(id) {
				folders[s.DBID(id)] = id
			}
		}
		ld.mu.Lock()
		d.store = s
		ld.mu.Unlock()
		return nil
	}
	lastSeal := time.Now()
	so.OnBatch = func(rows []index.StreamRow) {
		ld.mu.Lock()
		touched := map[uint32]bool{}
		for _, r := range rows {
			parent, ok := folders[r.Parent]
			if !ok {
				continue
			}
			id := d.store.Append(parent, r.Raw)
			if r.Raw.IsDir {
				folders[r.ID] = id
			}
			touched[parent] = true
		}
		for p := range touched {
			d.store.RepairSiblings(p)
		}
		ld.mu.Unlock()
		// An encrypted drive's index is worked on in a copy on this Mac:
		// written back now and then, so an unplug loses little.
		if d.encrypted && time.Since(lastSeal) > 10*time.Minute {
			d.sealBack()
			lastSeal = time.Now()
		}
	}
	start := time.Now().Unix()
	st, err := index.IndexStreaming(d.db, mount, d.scan, so)
	if err == nil && st.Unfinished == 0 {
		err = d.db.SetSetting(fullCheckKey, strconv.FormatInt(start, 10))
	}
	if err == nil {
		err = d.sealBack()
	}
	if err == nil {
		// Laid out afresh (compact and fast to search), with no lock held.
		var s *index.Store
		if s, err = d.load(); err == nil {
			ld.mu.Lock()
			d.store = s
			ld.mu.Unlock()
		}
	}
	if errors.Is(err, index.ErrInterrupted) && d.paused.Load() && !d.stop.Load() {
		// Stopped by the user: the drive stays attached and searchable
		// with what was read; Continue (or the next attach) goes on. Not
		// an error: the user asked for it.
		return st.IndexStats, nil
	}
	if err != nil {
		// Stopped or failed: what was saved stays on the drive (§9.7); the
		// drive is let go and the next attach resumes.
		ld.mu.Lock()
		if i := slices.Index(ld.drives, d); i >= 0 {
			d.closed = true
			d.close()
			ld.drives = slices.Delete(ld.drives, i, i+1)
		}
		ld.mu.Unlock()
	}
	return st.IndexStats, err
}

// AttachDrive opens an existing drive index (pass: for encrypted ones; ""
// uses a passphrase entered earlier this session). An interrupted first
// index resumes.
func (ld *Laserdot) AttachDrive(mount, pass string) (DriveInfo, error) {
	mount, err := canonical(mount)
	if err != nil {
		return DriveInfo{}, err
	}
	v, err := scan.VolumeAt(mount)
	if err != nil {
		return DriveInfo{}, err
	}
	ld.wmu.Lock()
	ld.mu.RLock()
	if ld.creating[mount] {
		ld.mu.RUnlock()
		ld.wmu.Unlock()
		return DriveInfo{}, fmt.Errorf("%s is being indexed", v.Name)
	}
	if ld.driveAt(mount) != nil {
		info := ld.driveInfo(v)
		ld.mu.RUnlock()
		ld.wmu.Unlock()
		return info, nil
	}
	dir, onDrive, vf, ok := ld.findIndex(v)
	if pass == "" {
		pass = ld.keys[vf.ID]
	}
	ld.mu.RUnlock()
	if !ok {
		ld.wmu.Unlock()
		return DriveInfo{}, fmt.Errorf("%s has no index yet", v.Name)
	}
	if vf.Encrypted && pass == "" {
		ld.wmu.Unlock()
		return DriveInfo{}, &DriveLockedError{v.Name}
	}
	db, work, err := ld.openDriveDB(dir, mount, vf.ID, pass, vf.Encrypted)
	if errors.Is(err, index.ErrLocked) {
		ld.wmu.Unlock()
		return DriveInfo{}, &DriveLockedError{v.Name}
	}
	if err != nil {
		ld.wmu.Unlock()
		return DriveInfo{}, err
	}
	ld.releasedMounts.Delete(mount) // attached again: its files may be read
	d := &drive{id: vf.ID, mount: mount, name: v.Name, dir: dir, onDrive: onDrive, encrypted: vf.Encrypted,
		pass: pass, db: db, work: work, scan: driveScanOptions(v)}
	if err := d.reload(); err != nil {
		d.close()
		ld.wmu.Unlock()
		return DriveInfo{}, err
	}
	d.release() // loaded: the drive's file is not kept open
	incomplete := !d.complete()
	ld.mu.Lock()
	if vf.Encrypted {
		ld.keys[vf.ID] = pass
	}
	ld.drives = append(ld.drives, d)
	ld.mu.Unlock()
	ld.wmu.Unlock()
	switch {
	case incomplete:
		// A first index that was cut short goes on where it stopped, in
		// the background: the drive is searchable meanwhile. One cut short
		// by a version before 0.9.19 has no folder checkpoints: it starts
		// over with the streaming indexer (bounded memory) rather than
		// being diffed whole, which held the whole drive in memory.
		ld.streams.Add(1)
		go func() {
			defer ld.streams.Done()
			defer crash.Guard("indexing a drive")
			ld.streamDrive(d, index.StreamOptions{})
		}()
	default:
		// A drive coming back: what changed while it was away is found in
		// the background (cheaply where folder times can be trusted).
		ld.streams.Add(1)
		go func() {
			defer ld.streams.Done()
			defer crash.Guard("indexing a drive")
			ld.CatchUpDrive(mount)
			ld.dropDriveJunk(d) // .DS_Store and the like from an index made before they were ignored
		}()
	}
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	return ld.driveInfo(v), nil
}

// StopDriveIndex stops a drive's first index (keeping what it read, the
// drive staying attached and searchable). False: none was running.
func (ld *Laserdot) StopDriveIndex(mount string) bool {
	if c, err := canonical(mount); err == nil {
		mount = c
	}
	ld.mu.RLock()
	d := ld.driveAt(mount)
	ld.mu.RUnlock()
	if d == nil || !d.indexing.Load() {
		return false
	}
	d.paused.Store(true)
	return true
}

// ContinueDriveIndex goes on with a drive's first index that was stopped
// (or cut short), in the background. False: nothing to continue.
func (ld *Laserdot) ContinueDriveIndex(mount string) bool {
	if c, err := canonical(mount); err == nil {
		mount = c
	}
	ld.mu.RLock()
	d := ld.driveAt(mount)
	ld.mu.RUnlock()
	if d == nil || d.indexing.Load() || d.complete() {
		return false
	}
	d.paused.Store(false)
	ld.streams.Add(1)
	go func() {
		defer ld.streams.Done()
		defer crash.Guard("indexing a drive")
		ld.streamDrive(d, index.StreamOptions{})
	}()
	return true
}

// release marks a drive as being let go (detached or ejected): long jobs
// (duplicate scans) stop reading files on it. Attaching it again clears it.
func (ld *Laserdot) release(mount string) { ld.releasedMounts.Store(mount, true) }

// released: path is on a drive being let go.
func (ld *Laserdot) released(path string) bool {
	found := false
	ld.releasedMounts.Range(func(k, _ any) bool {
		if within(path, k.(string)) {
			found = true
			return false
		}
		return true
	})
	return found
}

// RefreshDrive catches an attached drive's index up with the drive.
func (ld *Laserdot) RefreshDrive(mount string) (UpdateStats, error) {
	if c, err := canonical(mount); err == nil {
		mount = c
	}
	return ld.applyDrive(mount, []Change{{Path: mount, Recursive: true}}, true)
}

// FullCheckEvery: how long a drive's folder times are trusted before its
// whole tree is read again (CatchUpDrive).
var FullCheckEvery = 7 * 24 * time.Hour

// trustsFolderTimes: on these file systems adding, removing or renaming an
// entry always changes its folder's time. FAT, exFAT and NTFS (as macOS
// mounts them) do not promise it, and neither does an unknown one.
var trustsFolderTimes = func(fsType string) bool { return fsType == "apfs" || fsType == "hfs" }

// fullCheckKey: when the drive's tree was last read whole (its own index's
// settings, so it travels with the drive).
const fullCheckKey = "full_check"

// CatchUpDrive brings an attached drive's index up to date as cheaply as it
// safely can (a returning drive, the hourly catch-up). On APFS and HFS+,
// with a full read in the last FullCheckEvery, only folders whose time
// changed are listed again: one lstat per folder instead of a listing of
// every file, minutes saved on a big drive. Elsewhere, or when it is time,
// the whole tree is read (RefreshDrive). Changes inside files that keep the
// folder's time wait for that weekly full read. A first index still running
// is left alone; one with folders left unread goes on.
func (ld *Laserdot) CatchUpDrive(mount string) (UpdateStats, error) {
	if c, err := canonical(mount); err == nil {
		mount = c
	}
	ld.mu.RLock()
	d := ld.driveAt(mount)
	ld.mu.RUnlock()
	if d == nil {
		return UpdateStats{OfflineRoots: []string{}}, errors.New("drive not attached")
	}
	if d.indexing.Load() {
		return UpdateStats{OfflineRoots: []string{}}, nil
	}
	if !d.complete() && d.paused.Load() {
		return UpdateStats{OfflineRoots: []string{}}, nil // stopped by the user: waits for Continue
	}
	if !d.complete() {
		// A first index not finished (a stuck folder, or cut short): it
		// goes on with the streaming indexer.
		st, err := ld.streamDrive(d, index.StreamOptions{})
		return UpdateStats{Added: st.Files + st.Folders, OfflineRoots: []string{}}, err
	}
	v, err := scan.VolumeAt(mount)
	if err != nil {
		return UpdateStats{OfflineRoots: []string{}}, err
	}
	d.wmu.Lock()
	var last string
	if err := d.acquire(); err == nil {
		last, _ = d.db.Setting(fullCheckKey)
		d.release()
	}
	d.wmu.Unlock()
	at, _ := strconv.ParseInt(last, 10, 64)
	if trustsFolderTimes(v.FSType) && at > 0 && time.Since(time.Unix(at, 0)) < FullCheckEvery {
		stale := ld.staleIn(func() *index.Store { return d.store })
		changes := make([]Change, len(stale))
		for i, p := range stale {
			changes[i] = Change{Path: p}
		}
		if len(changes) == 0 {
			return UpdateStats{OfflineRoots: []string{}}, nil
		}
		return ld.applyDrive(mount, changes, false)
	}
	return ld.applyDrive(mount, []Change{{Path: mount, Recursive: true}}, true)
}

// applyDrive applies changes to the drive at mount. Caller holds no lock.
// Only this drive's writer lock is held while the drive is looked at (an 8 TB
// drive takes minutes): searches, this Mac's own updates and other drives
// carry on; searches wait only while the in-memory index changes.
func (ld *Laserdot) applyDrive(mount string, changes []Change, complete bool) (UpdateStats, error) {
	ld.mu.RLock()
	d := ld.driveAt(mount)
	ld.mu.RUnlock()
	if d == nil {
		return UpdateStats{}, errors.New("drive not attached")
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if d.closed {
		return UpdateStats{OfflineRoots: []string{}}, errors.New("drive not attached")
	}
	if err := d.acquire(); err != nil {
		return UpdateStats{OfflineRoots: []string{}}, err
	}
	defer d.release()
	start := time.Now().Unix()
	opts := d.scan
	opts.Stop = d.stop.Load // ejecting lets go at the next folder
	u, err := index.Apply(d.db, d.store, changes, opts, &ld.mu)
	if err == nil && complete {
		// The whole tree was read: complete, and the time of this full
		// read noted for CatchUpDrive.
		err = d.db.FinishRoot(mount)
		if err == nil {
			err = d.db.SetSetting(fullCheckKey, strconv.FormatInt(start, 10))
		}
	}
	if err == nil && d.store.NeedsCompaction() {
		var s *index.Store
		if s, err = d.load(); err == nil {
			ld.mu.Lock()
			d.store = s
			ld.mu.Unlock()
		}
	}
	if u.OfflineRoots == nil {
		u.OfflineRoots = []string{}
	}
	if err == nil {
		err = d.sealBack()
	}
	return u, err
}

// DetachDrive stops using a drive's index (its results disappear).
func (ld *Laserdot) DetachDrive(mount string) bool {
	if c, err := canonical(mount); err == nil {
		mount = c
	}
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	return ld.detach(mount)
}

func (ld *Laserdot) detach(mount string) bool {
	ld.mu.RLock()
	d := ld.driveAt(mount)
	ld.mu.RUnlock()
	if d == nil {
		return false
	}
	// A first index running stops between batches, a duplicate scan skips
	// the drive's files; then the drive's writer lock (lock order: drive,
	// then the index).
	ld.release(mount)
	d.stop.Store(true)
	d.wmu.Lock()
	defer d.wmu.Unlock()
	ld.mu.Lock()
	defer ld.mu.Unlock()
	for i, x := range ld.drives {
		if x == d {
			d.closed = true
			d.close()
			ld.drives = slices.Delete(ld.drives, i, i+1)
			return true
		}
	}
	// A first index it stopped let the drive go already.
	return d.closed
}

// DeleteDriveIndex detaches and deletes a drive's index.
func (ld *Laserdot) DeleteDriveIndex(mount string) (bool, error) {
	mount, err := canonical(mount)
	if err != nil {
		return false, err
	}
	ld.DetachDrive(mount)
	v, err := scan.VolumeAt(mount)
	if err != nil {
		return false, err
	}
	ld.mu.RLock()
	dir, _, _, ok := ld.findIndex(v)
	ld.mu.RUnlock()
	if !ok {
		return false, nil
	}
	return true, os.RemoveAll(dir)
}

// RebuildDrive deletes and recreates a drive's index.
func (ld *Laserdot) RebuildDrive(mount string, opts DriveOptions) (IndexStats, error) {
	if _, err := ld.DeleteDriveIndex(mount); err != nil {
		return IndexStats{}, err
	}
	return ld.CreateDriveIndex(mount, opts)
}

// EjectDrive detaches the drive and ejects it (macOS).
func (ld *Laserdot) EjectDrive(mount string) error {
	mount, err := canonical(mount)
	if err != nil {
		return err
	}
	ld.DetachDrive(mount)
	if runtime.GOOS != "darwin" {
		return errors.New("ejecting is only supported on macOS")
	}
	if out, err := exec.Command("/usr/sbin/diskutil", "eject", mount).CombinedOutput(); err != nil {
		return fmt.Errorf("diskutil could not eject %s: %s", mount, out)
	}
	return nil
}

// SyncDrives follows mounts: drives that went away are detached; mounted
// drives with an index (and a known passphrase, if encrypted) attach.
func (ld *Laserdot) SyncDrives() (attached, gone []string) {
	// Looking at the drives happens without the lock: a drive that stops
	// answering must not hold searches up.
	ld.mu.RLock()
	vols := ld.volumes
	var mounts []string
	for _, d := range ld.drives {
		mounts = append(mounts, d.mount)
	}
	ld.mu.RUnlock()
	mounted := scan.ListVolumes(vols.Dir, vols.MountsOnly)
	for _, m := range mounts {
		_, err := os.Stat(m)
		if err != nil || !slices.ContainsFunc(mounted, func(v scan.Volume) bool { return v.Mount == m }) {
			gone = append(gone, m)
		}
	}
	for _, m := range gone {
		ld.DetachDrive(m)
	}
	for _, v := range mounted {
		_, _, vf, ok := ld.findIndex(v) // reads the drive: without the lock
		ld.mu.RLock()
		skip := ld.driveAt(v.Mount) != nil || ld.creating[v.Mount]
		_, haveKey := ld.keys[vf.ID]
		ld.mu.RUnlock()
		if skip || !ok || vf.Encrypted && !haveKey {
			continue // a locked drive waits for its passphrase
		}
		if _, err := ld.AttachDrive(v.Mount, ""); err == nil {
			attached = append(attached, v.Mount)
		}
	}
	return attached, gone
}

// unindexedVolume is the mounted volume holding path, if any (drives keep
// their own index, so the system index never crosses into one, §17.6.1).
func (ld *Laserdot) unindexedVolume(path string) (scan.Volume, bool) {
	ld.mu.RLock()
	vc := ld.volumes
	ld.mu.RUnlock()
	for _, v := range scan.ListVolumes(vc.Dir, vc.MountsOnly) { // the disks, without the lock
		if within(path, v.Mount) {
			return v, true
		}
	}
	return scan.Volume{}, false
}

func (ld *Laserdot) closeDrives() {
	ld.mu.RLock()
	drives := slices.Clone(ld.drives)
	ld.mu.RUnlock()
	for _, d := range drives {
		d.stop.Store(true)
	}
	for _, d := range drives {
		d.wmu.Lock()
		ld.mu.Lock()
		if !d.closed {
			d.closed = true
			d.close()
		}
		ld.mu.Unlock()
		d.wmu.Unlock()
	}
	ld.mu.Lock()
	ld.drives = nil
	ld.mu.Unlock()
}

// PurgeOffDriveIndexes deletes the indexes kept on this Mac (drives that
// were read-only) of drives that are not mounted now: an index of a drive
// should not stay behind once the drive is gone (the owner's rule, for
// confidentiality). Indexes on a drive leave with it and are not touched.
// laserdotd runs it at start, every minute, after mounts change, and when
// it quits. Returns the folders deleted.
func (ld *Laserdot) PurgeOffDriveIndexes() []string {
	base := ld.drivesDataDir()
	des, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	ld.mu.RLock()
	mounted := scan.ListVolumes(ld.volumes.Dir, ld.volumes.MountsOnly)
	keep := map[string]bool{}
	for _, v := range mounted {
		keep[volumeKey(v)] = true
	}
	attached := 0
	for _, d := range ld.drives {
		if !d.onDrive {
			keep[filepath.Base(d.dir)] = true
		}
		attached++
	}
	ld.mu.RUnlock()
	// No volumes listed while drives are attached: the listing failed, not
	// every drive left. Wait for the next check rather than delete.
	if len(mounted) == 0 && attached > 0 {
		return nil
	}
	var removed []string
	for _, de := range des {
		if !de.IsDir() || keep[de.Name()] {
			continue
		}
		p := filepath.Join(base, de.Name())
		if os.RemoveAll(p) == nil {
			removed = append(removed, p)
		}
	}
	return removed
}
