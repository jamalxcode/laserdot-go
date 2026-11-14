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
	LastScan   *int64     `json:"last_scan"`
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
}

// DriveOptions for creating a drive index.
type DriveOptions struct {
	// Passphrase encrypts the index (§9.8); "" = not encrypted.
	Passphrase string
	// ForceOffDrive keeps the index on this Mac even if the drive is
	// writable.
	ForceOffDrive bool
	Bulk          index.BulkOptions
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
	db                   *index.Db
	// work: the decrypted working copy on this Mac (encrypted drives).
	work  string
	store *index.Store
	scan  scan.Options
}

func (d *drive) indexPath() string { return filepath.Join(d.dir, "index.db") }

func (d *drive) reload() error {
	raw, err := d.db.Load()
	if err != nil {
		return err
	}
	s := index.Build(raw)
	s.SetVolumeName(d.name)
	d.store = s
	return nil
}

// sealBack writes an encrypted drive's working copy back to the drive.
func (d *drive) sealBack() error {
	if !d.encrypted {
		return nil
	}
	if err := d.db.Checkpoint(); err != nil {
		return err
	}
	return index.Seal(d.work, d.indexPath(), d.pass)
}

func (d *drive) close() {
	d.sealBack()
	d.db.Close()
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
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	vols := scan.ListVolumes(ld.volumes.Dir, ld.volumes.MountsOnly)
	for _, d := range ld.drives {
		if !slices.ContainsFunc(vols, func(v scan.Volume) bool { return v.Mount == d.mount }) {
			if v, err := scan.VolumeAt(d.mount); err == nil {
				vols = append(vols, v)
			}
		}
	}
	out := []DriveInfo{}
	for _, v := range vols {
		out = append(out, ld.driveInfo(v))
	}
	return out
}

func (ld *Laserdot) driveInfo(v scan.Volume) DriveInfo {
	info := DriveInfo{
		Name: v.Name, Mount: v.Mount, FSType: v.FSType, ReadOnly: v.ReadOnly, TotalBytes: v.TotalBytes,
		FreeBytes: v.FreeBytes, State: DriveState{State: DriveUnindexed},
	}
	if d := ld.driveAt(v.Mount); d != nil {
		roots, _ := d.db.Roots()
		id, p := d.id, d.indexPath()
		info.ID, info.IndexPath = &id, &p
		info.State = DriveState{State: DriveAttached}
		info.OnDrive, info.Encrypted = d.onDrive, d.encrypted
		info.IndexBytes = dirBytes(d.dir)
		info.Entries = d.store.LiveCount()
		info.Complete = true
		for _, r := range roots {
			info.Complete = info.Complete && r.Complete
			if info.LastScan == nil || r.LastVerified > *info.LastScan {
				lv := r.LastVerified
				info.LastScan = &lv
			}
		}
	} else if dir, on, vf, ok := ld.findIndex(v); ok {
		p := filepath.Join(dir, "index.db")
		info.ID, info.IndexPath = &vf.ID, &p
		info.OnDrive, info.Encrypted = on, vf.Encrypted
		info.IndexBytes = dirBytes(dir)
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
	files, dirs, bytes, err := scan.Estimate(mount, driveScanOptions(v))
	if err != nil {
		return DrivePlan{}, err
	}
	// Estimate counts the mount itself as a folder.
	dirs = max(dirs, 1) - 1
	p := DrivePlan{Name: v.Name, Mount: mount, Files: files, Folders: dirs, Bytes: bytes,
		IndexEstimateBytes: (files + dirs) * indexBytesPerEntry, OnDrive: !v.ReadOnly}
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
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	ld.mu.RLock()
	_, _, _, exists := ld.findIndex(v)
	exists = exists || ld.driveAt(mount) != nil
	ld.mu.RUnlock()
	if exists {
		return IndexStats{}, fmt.Errorf("%s already has an index — rebuild it instead", mount)
	}
	dir, onDrive := filepath.Join(mount, ".laserdot"), true
	if opts.ForceOffDrive || v.ReadOnly || os.MkdirAll(dir, 0o755) != nil {
		dir, onDrive = ld.offDriveDir(v), false
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return IndexStats{}, err
		}
	}
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
		ld.keys[vf.ID] = opts.Passphrase
	}
	// The menu shows how far it has got, as for a folder on this Mac.
	bulk := opts.Bulk
	if bulk.Progress == nil {
		bulk.Progress = func(phase string, done, total uint64) {
			ld.progress.Store(&IndexProgress{Root: mount, Phase: phase, Done: done, Total: total})
		}
		defer ld.progress.Store(nil)
	}
	st, err := index.IndexRoot(db, mount, d.scan, bulk)
	if err != nil {
		d.close() // an interrupted index keeps its committed prefix (§9.7)
		return st, err
	}
	if err := d.sealBack(); err != nil {
		d.close()
		return st, err
	}
	if err := d.reload(); err != nil {
		d.close()
		return st, err
	}
	ld.mu.Lock()
	ld.drives = append(ld.drives, d)
	ld.mu.Unlock()
	return st, nil
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
	d := &drive{id: vf.ID, mount: mount, name: v.Name, dir: dir, onDrive: onDrive, encrypted: vf.Encrypted,
		pass: pass, db: db, work: work, scan: driveScanOptions(v)}
	if err := d.reload(); err != nil {
		d.close()
		ld.wmu.Unlock()
		return DriveInfo{}, err
	}
	roots, _ := db.Roots()
	incomplete := slices.ContainsFunc(roots, func(r index.RootInfo) bool { return !r.Complete })
	ld.mu.Lock()
	if vf.Encrypted {
		ld.keys[vf.ID] = pass
	}
	ld.drives = append(ld.drives, d)
	ld.mu.Unlock()
	ld.wmu.Unlock()
	if incomplete {
		if _, err := ld.RefreshDrive(mount); err != nil {
			return DriveInfo{}, err
		}
	}
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	return ld.driveInfo(v), nil
}

// RefreshDrive catches an attached drive's index up with the drive.
func (ld *Laserdot) RefreshDrive(mount string) (UpdateStats, error) {
	if c, err := canonical(mount); err == nil {
		mount = c
	}
	return ld.applyDrive(mount, []Change{{Path: mount, Recursive: true}}, true)
}

// applyDrive applies changes to the drive at mount. Caller holds no lock.
func (ld *Laserdot) applyDrive(mount string, changes []Change, complete bool) (UpdateStats, error) {
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	ld.mu.Lock()
	d := ld.driveAt(mount)
	if d == nil {
		ld.mu.Unlock()
		return UpdateStats{}, errors.New("drive not attached")
	}
	u, err := index.Apply(d.db, d.store, changes, d.scan)
	if err == nil && complete {
		err = d.db.FinishRoot(mount)
	}
	if err == nil && d.store.NeedsCompaction() {
		err = d.reload()
	}
	ld.mu.Unlock()
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
	ld.mu.Lock()
	defer ld.mu.Unlock()
	for i, d := range ld.drives {
		if d.mount == mount {
			d.close()
			ld.drives = slices.Delete(ld.drives, i, i+1)
			return true
		}
	}
	return false
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
	ld.mu.RLock()
	mounted := scan.ListVolumes(ld.volumes.Dir, ld.volumes.MountsOnly)
	for _, d := range ld.drives {
		_, err := os.Stat(d.mount)
		if err != nil || !slices.ContainsFunc(mounted, func(v scan.Volume) bool { return v.Mount == d.mount }) {
			gone = append(gone, d.mount)
		}
	}
	ld.mu.RUnlock()
	for _, m := range gone {
		ld.DetachDrive(m)
	}
	for _, v := range mounted {
		ld.mu.RLock()
		skip := ld.driveAt(v.Mount) != nil
		_, _, vf, ok := ld.findIndex(v)
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
	defer ld.mu.RUnlock()
	for _, v := range scan.ListVolumes(ld.volumes.Dir, ld.volumes.MountsOnly) {
		if within(path, v.Mount) {
			return v, true
		}
	}
	return scan.Volume{}, false
}

func (ld *Laserdot) closeDrives() {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	for _, d := range ld.drives {
		d.close()
	}
	ld.drives = nil
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
