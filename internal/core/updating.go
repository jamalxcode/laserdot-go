package core

// Applying changes, refreshing, checking for missed changes; roots and stats.

import (
	"os"
	"path/filepath"

	"github.com/jamalxcode/laserdot-go/internal/index"
)

// currentEventID is the platform's change-journal position, set by the
// watcher package on macOS (FSEvents); none elsewhere.
var currentEventID = func() (uint64, bool) { return 0, false }

// ApplyChanges applies "something changed under this path" signals;
// changes on an attached drive go to that drive's index.
func (ld *Laserdot) ApplyChanges(changes []Change) (UpdateStats, error) {
	byDrive := map[string][]Change{}
	var local []Change
	ld.mu.RLock()
	for _, c := range changes {
		if d := ld.driveFor(c.Path); d != nil {
			byDrive[d.mount] = append(byDrive[d.mount], c)
		} else {
			local = append(local, c)
		}
	}
	ld.mu.RUnlock()
	total := UpdateStats{OfflineRoots: []string{}}
	for mount, cs := range byDrive {
		u, err := ld.applyDrive(mount, cs, false)
		total.Merge(u)
		if err != nil {
			return total, err
		}
	}
	if len(local) == 0 && len(byDrive) > 0 {
		return total, nil
	}
	u, err := ld.applyLocal(local)
	total.Merge(u)
	total.MS = u.MS
	return total, err
}

func (ld *Laserdot) applyLocal(changes []Change) (UpdateStats, error) {
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	// Searches go on while the disk is looked at (wmu keeps other writers
	// out); they wait only while the in-memory index changes.
	u, err := index.Apply(ld.db, ld.store, changes, ld.ScanOptions(), &ld.mu)
	compact := err == nil && ld.store.NeedsCompaction()
	if u.OfflineRoots == nil {
		u.OfflineRoots = []string{}
	}
	if compact {
		err = ld.reload()
	}
	return u, err
}

// PruneLibrary removes from the index what ~/Library holds besides iCloud
// Drive (caches, app data), when ~/Library is inside an indexed folder. Such
// entries come from versions that indexed all of it; listing ~/Library
// again with today's rules leaves them out. A no-op when nothing is there.
func (ld *Laserdot) PruneLibrary() (UpdateStats, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return UpdateStats{OfflineRoots: []string{}}, nil
	}
	lib := filepath.Join(home, "Library")
	if c, err := canonical(lib); err == nil {
		lib = c
	}
	ld.mu.RLock()
	id, ok := ld.store.Resolve(lib)
	extra := false
	if ok && !ld.store.IsRoot(id) {
		for _, c := range ld.store.Children(id) {
			if filepath.Base(ld.store.Path(c)) != "Mobile Documents" {
				extra = true
				break
			}
		}
	}
	ld.mu.RUnlock()
	if !extra {
		return UpdateStats{OfflineRoots: []string{}}, nil
	}
	return ld.applyLocal([]Change{{Path: lib, Recursive: false}})
}

// Refresh catches up on changes made while not watching: every indexed
// folder is rescanned and diffed in place, and every attached drive caught
// up (CatchUpDrive: by its folders' times where they can be trusted).
func (ld *Laserdot) Refresh() (UpdateStats, error) {
	ld.wmu.Lock()
	roots, err := ld.db.Roots()
	ld.wmu.Unlock()
	if err != nil {
		return UpdateStats{OfflineRoots: []string{}}, err
	}
	var changes []Change
	var unfinished []string
	for _, r := range roots {
		if !r.Complete {
			unfinished = append(unfinished, r.Path)
			continue
		}
		changes = append(changes, Change{Path: r.Path, Recursive: true})
	}
	// First indexes that never finished start again (this runs when the
	// daemon starts, so it heals by itself after a crash).
	offline := []string{}
	for _, p := range unfinished {
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			// Gone mid-index (a drive pulled out, a folder deleted): it
			// goes on when it is back; the rest is refreshed meanwhile.
			offline = append(offline, p)
			continue
		}
		if _, err := ld.Index(p); err != nil {
			return UpdateStats{OfflineRoots: []string{}}, err
		}
	}
	u, err := ld.applyLocal(changes)
	u.OfflineRoots = append(u.OfflineRoots, offline...)
	if err != nil {
		return u, err
	}
	ld.mu.RLock()
	var mounts []string
	for _, d := range ld.drives {
		mounts = append(mounts, d.mount)
	}
	ld.mu.RUnlock()
	for _, m := range mounts {
		du, err := ld.CatchUpDrive(m)
		if err != nil {
			return u, err
		}
		u.Merge(du)
	}
	return u, nil
}

// QuickCheck compares every indexed folder's modification time with the
// disk and lists again only the folders that differ. Adding, removing or
// renaming a file changes its folder's time, so this finds what the file
// events missed for one lstat per folder and none per file (a tenth of a
// full Refresh, or less); changes inside files wait for the hourly Refresh.
func (ld *Laserdot) QuickCheck() (UpdateStats, error) {
	stale := ld.staleFolders()
	if len(stale) == 0 {
		return UpdateStats{OfflineRoots: []string{}}, nil
	}
	changes := make([]Change, len(stale))
	for i, p := range stale {
		changes[i] = Change{Path: p}
	}
	return ld.applyLocal(changes)
}

// staleFolders: indexed folders whose time on disk is not the indexed one.
// A folder that is gone is left to its parent (whose time changed too); a
// root that is gone is an offline root, kept as it is.
func (ld *Laserdot) staleFolders() []string {
	return ld.staleIn(func() *index.Store { return ld.store })
}

// staleIn: staleFolders for a store (this Mac's or a drive's), read under
// the index lock; the disk is looked at with no lock held.
func (ld *Laserdot) staleIn(store func() *index.Store) []string {
	type folder struct {
		path string
		mod  int64
	}
	var folders []folder
	ld.mu.RLock()
	s := store()
	it := s.Live().Iterator()
	for it.HasNext() {
		id := it.Next()
		if s.IsDir(id) && !s.IsSymlink(id) {
			folders = append(folders, folder{s.Path(id), s.Modified(id)})
		}
	}
	ld.mu.RUnlock()
	var stale []string
	for _, f := range folders {
		info, err := os.Lstat(f.path)
		if err != nil || !info.IsDir() {
			continue
		}
		if info.ModTime().Unix() != f.mod {
			stale = append(stale, f.path)
		}
	}
	return stale
}

// CheckPending re-examines files that were still being written; any that
// are now stable get probed (§2.6.3).
func (ld *Laserdot) CheckPending() (UpdateStats, error) {
	if ld.probing.Load() {
		// The background pass is reading a first index's headers; the files
		// being written wait until it is done.
		return UpdateStats{OfflineRoots: []string{}}, nil
	}
	ld.mu.RLock()
	var changes []Change
	seen := map[string]bool{}
	it := ld.store.Pending().Iterator()
	for it.HasNext() {
		id := it.Next()
		p := filepath.Dir(ld.store.Path(id))
		if !seen[p] {
			seen[p] = true
			changes = append(changes, Change{Path: p})
		}
	}
	ld.mu.RUnlock()
	if len(changes) == 0 {
		return UpdateStats{OfflineRoots: []string{}}, nil
	}
	return ld.ApplyChanges(changes)
}

// SetEventID records the last applied change-journal id of a root.
func (ld *Laserdot) SetEventID(root string, id uint64) error {
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	return ld.db.SetEventID(root, id)
}

// Forget removes an indexed folder. False when it was not indexed.
func (ld *Laserdot) Forget(root string) (bool, error) {
	if c, err := canonical(root); err == nil {
		root = c
	} else {
		root = filepath.Clean(root)
	}
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	ok, err := ld.db.RemoveRoot(root)
	if err != nil {
		return false, err
	}
	return ok, ld.reload()
}

// Roots are the indexed folders with their state.
func (ld *Laserdot) Roots() ([]RootInfo, error) {
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	return ld.db.Roots()
}

// Stats describes the loaded index.
func (ld *Laserdot) Stats() Stats {
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	s := ld.store
	st := Stats{
		Entries: s.LiveCount(), Roots: s.Roots(), MemoryBytes: s.MemoryBytes(),
		LoadMS: ld.loadMS, Overlay: s.OverlaySize(), Drives: []string{},
		FromSnapshot: s.FromSnapshot(), MappedBytes: s.MappedBytes(),
	}
	for _, d := range ld.drives {
		st.Entries += d.store.LiveCount()
		st.Roots = append(st.Roots, d.mount)
		st.MemoryBytes += d.store.MemoryBytes()
		st.Drives = append(st.Drives, d.name)
	}
	return st
}
