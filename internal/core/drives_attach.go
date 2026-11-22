package core

// Attaching a drive, keeping it current, letting it go (detach, delete, rebuild, eject, sync).

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/crash"
	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/scan"
)

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
