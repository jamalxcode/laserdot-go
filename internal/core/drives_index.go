package core

// Planning and building a drive's index: the first index, streamed; stop and continue.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/scan"
)

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
