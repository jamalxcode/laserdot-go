package core

// Listing the volumes and what is known of each drive (read without the index lock).

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/scan"
)

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
	out := []DriveInfo{}
	for i, v := range vols {
		// macOS's own mounts (a disk image named by a long id, Recovery…)
		// are not drives to offer (the owner: "I have no idea what drive
		// this is") — unless it has an index or is being indexed.
		if v.Hidden && !found[i].ok && attached[v.Mount] == "" && !ld.creating[v.Mount] {
			continue
		}
		out = append(out, ld.driveInfoFrom(v, found[i]))
	}
	ld.mu.RUnlock()
	seen := make([]mountedDrive, 0, len(out))
	for _, d := range out {
		seen = append(seen, mountedDrive{name: d.Name, mount: d.Mount, state: d.State.State})
	}
	ld.mountedMu.Lock()
	ld.mounted = seen
	ld.mountedMu.Unlock()
	return out
}

// mountedDrive: a drive seen mounted, and its state then (unindexed,
// detached, locked, attached…).
type mountedDrive struct{ name, mount, state string }

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
