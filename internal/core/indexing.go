package core

// Indexing this Mac's folders, and the details read in the background.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/crash"
	"github.com/jamalxcode/laserdot-go/internal/media"

	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// canonical resolves path to an absolute path without symlinks.
func canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func within(path, root string) bool {
	sep := string(os.PathSeparator)
	return path == root || strings.HasPrefix(path, strings.TrimSuffix(root, sep)+sep)
}

// ScanOptions are the defaults plus the index's own files (the index never
// indexes itself).
func (ld *Laserdot) ScanOptions() scan.Options {
	opts := scan.DefaultOptions()
	dir, base := filepath.Split(ld.dbPath)
	if c, err := canonical(dir); err == nil {
		dir = c
	}
	for _, b := range []string{base, "content.db", "archives.db"} {
		for _, suffix := range []string{"", "-wal", "-shm", "-journal", ".roots", ".snapshot", ".snapshot.tmp"} {
			opts.ExcludedPaths = append(opts.ExcludedPaths, filepath.Join(dir, b+suffix))
		}
	}
	// ~/Library is app data, caches and settings, not the user's files:
	// only iCloud Drive (Mobile Documents) is indexed there, as Spotlight
	// shows it. Indexing the home folder therefore stays useful and small.
	if home, err := os.UserHomeDir(); err == nil {
		lib := filepath.Join(home, "Library")
		if c, err := canonical(lib); err == nil {
			lib = c
		}
		opts.OnlyChildren = map[string][]string{lib: {"Mobile Documents"}}
	}
	opts.ExcludedPaths = append(opts.ExcludedPaths, ld.loadExclusions()...)
	return opts
}

// Index indexes a folder. A new folder is walked in bulk; a folder that is
// already indexed (or inside one) is refreshed in place, so renames are
// tracked and unchanged files are not touched.
func (ld *Laserdot) Index(root string) (IndexStats, error) {
	st, err := ld.index(root)
	if syncBackground {
		ld.background.Wait() // outside wmu: the passes take it
	}
	return st, err
}

func (ld *Laserdot) index(root string) (IndexStats, error) {
	root, err := canonical(root)
	if err != nil {
		return IndexStats{}, err
	}
	if err := enoughSpace(ld.dbDir()); err != nil {
		return IndexStats{}, err
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return IndexStats{}, fmt.Errorf("%s is not a folder", root)
	}
	ld.mu.RLock()
	d := ld.driveFor(root)
	ld.mu.RUnlock()
	if d != nil {
		// On an attached drive: refresh that drive's index in place.
		u, err := ld.RefreshDrive(d.mount)
		return IndexStats{Root: root, Incremental: true, Added: u.Added, Removed: u.Removed, Renamed: u.Renamed,
			Modified: u.Modified, Pending: u.Pending, Probed: u.Probed}, err
	}
	if v, ok := ld.unindexedVolume(root); ok {
		return IndexStats{}, fmt.Errorf("%s is on the drive %q, which keeps its own index — index the drive instead", root, v.Name)
	}
	ld.wmu.Lock()
	roots, err := ld.db.Roots()
	ld.wmu.Unlock()
	if err != nil {
		return IndexStats{}, err
	}
	for _, r := range roots {
		// A folder whose first index never finished (Laserdot was quit or
		// crashed meanwhile) is indexed again from the start, not updated:
		// its files may not be in the index at all.
		if within(root, r.Path) && r.Complete {
			u, err := ld.ApplyChanges([]Change{{Path: root, Recursive: true}})
			return IndexStats{
				Root: root, Incremental: true, Added: u.Added, Removed: u.Removed, Renamed: u.Renamed,
				Modified: u.Modified, Pending: u.Pending, Probed: u.Probed, Reused: u.Reused,
				ProbeFailed: u.ProbeFailed, WriteMS: u.MS,
			}, err
		}
	}
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	defer ld.progress.Store(nil)
	var fresh []index.RawEntry
	var unreadable []scan.Error
	opts := ld.ScanOptions()
	opts.DeferTags = true // read by readTagsLater, once the files are searchable
	st, err := index.IndexRoot(ld.db, root, opts, index.BulkOptions{
		ProbeLater: true, // media headers: probeLater, once the files are searchable
		Progress: func(phase string, done, total uint64) {
			ld.progress.Store(&IndexProgress{Root: root, Phase: phase, Done: done, Total: total})
		},
		Raw:    &fresh,
		Errors: &unreadable,
	})
	ld.noteUnreadable(st.Root, unreadable)
	if err != nil {
		return st, err
	}
	if id, ok := currentEventID(); ok {
		if err := ld.db.SetEventID(st.Root, id); err != nil {
			return st, err
		}
	}
	if err := ld.reloadWith(st.Root, fresh); err != nil {
		return st, err
	}
	ld.readTagsLater(st.Root)
	ld.probeLater()
	return st, nil
}

// syncBackground: Index returns only once its background passes (tags,
// media headers) are done. For tests that check those right after.
var syncBackground = false

// probeChunk: headers read and stored per step of the background pass.
const probeChunk = 20_000

// probeLater reads, in the background, the media headers a first index left
// for later (pending files that are not being written): in parallel, a
// chunk at a time, straight into the database; then the in-memory index is
// rebuilt once from it and served from its snapshot. Name searches work all
// along; media filters (res:, codec:…) find the files once this is done.
func (ld *Laserdot) probeLater() {
	if !ld.probing.CompareAndSwap(false, true) {
		return // a pass is running
	}
	ld.background.Add(1)
	go func() {
		defer ld.background.Done()
		defer ld.probing.Store(false)
		defer crash.Guard("reading picture and video details")
		type job struct {
			db   int64
			path string
			size uint64
		}
		// Which ones: ids only for now (paths a chunk at a time).
		ld.mu.RLock()
		var ids []uint32
		it := ld.store.Pending().Iterator()
		for it.HasNext() {
			id := it.Next()
			// Media files written moments ago wait; documents are read at once
			// (index.ShouldDefer says why).
			fresh := time.Now().Unix()-ld.store.Modified(id) < index.StableSecs && ld.store.Kind(id) != media.Document
			if !ld.store.IsDataless(id) && !fresh {
				ids = append(ids, id)
			}
		}
		ld.mu.RUnlock()
		if len(ids) == 0 {
			return
		}
		defer ld.details.Store(nil)
		var done atomic.Uint64
		total := uint64(len(ids))
		ld.details.Store(&IndexProgress{Phase: "details", Total: total})
		wrote := false
		for start := 0; start < len(ids); start += probeChunk {
			chunk := ids[start:min(len(ids), start+probeChunk)]
			jobs := make([]job, 0, len(chunk))
			ld.mu.RLock()
			for _, id := range chunk {
				jobs = append(jobs, job{ld.store.DBID(id), ld.store.Path(id), ld.store.Size(id)})
			}
			ld.mu.RUnlock()
			probes := make([]index.Probe, len(jobs))
			var next atomic.Int64
			var wg sync.WaitGroup
			for w := 0; w < max(8, 2*runtime.GOMAXPROCS(0)); w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						i := int(next.Add(1)) - 1
						if i >= len(jobs) {
							return
						}
						probes[i] = index.ProbePath(jobs[i].path, jobs[i].size)
						if n := done.Add(1); n%500 == 0 {
							ld.details.Store(&IndexProgress{Phase: "details", Done: n, Total: total})
						}
					}
				}()
			}
			wg.Wait()
			dbIDs := make([]int64, len(jobs))
			for i, j := range jobs {
				dbIDs[i] = j.db
			}
			ld.wmu.Lock()
			err := ld.db.SetProbes(dbIDs, probes)
			ld.wmu.Unlock()
			if err != nil {
				return
			}
			wrote = true
		}
		if !wrote {
			return
		}
		// One rebuild with every header, then file-backed pages again.
		ld.wmu.Lock()
		err := ld.reload()
		ld.wmu.Unlock()
		if err == nil {
			_ = ld.Remap()
		}
	}()
}

func (ld *Laserdot) pendingCount() uint64 {
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	return ld.store.Pending().GetCardinality()
}

// readTagsLater reads the Finder tags of a folder just indexed without them
// (one extended-attribute read per file, a large share of a walk on macOS)
// and brings in those it finds: each folder holding a tagged file is listed
// again, which records its tags (§2.6.5). Runs in the background.
func (ld *Laserdot) readTagsLater(root string) {
	// Ids only (4 bytes each); paths are made a chunk at a time.
	ld.mu.RLock()
	var ids []uint32
	if id, ok := ld.store.Resolve(root); ok {
		for _, c := range ld.store.Descendants(id) {
			if !ld.store.IsDir(c) {
				ids = append(ids, c)
			}
		}
	}
	ld.mu.RUnlock()
	if len(ids) == 0 {
		return
	}
	ld.background.Add(1)
	go func() {
		defer ld.background.Done()
		defer crash.Guard("reading Finder tags")
		dirs := map[string]bool{}
		const chunk = 20_000
		for start := 0; start < len(ids); start += chunk {
			part := ids[start:min(len(ids), start+chunk)]
			paths := make([]string, len(part))
			ld.mu.RLock()
			for i, id := range part {
				paths[i] = ld.store.Path(id)
			}
			ld.mu.RUnlock()
			var mu sync.Mutex
			var next atomic.Int64
			var wg sync.WaitGroup
			// Mostly waiting on the disk: more readers than cores.
			for w := 0; w < max(16, 4*runtime.GOMAXPROCS(0)); w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						i := int(next.Add(1)) - 1
						if i >= len(paths) {
							return
						}
						if len(scan.ReadTags(paths[i])) > 0 {
							mu.Lock()
							dirs[filepath.Dir(paths[i])] = true
							mu.Unlock()
						}
					}
				}()
			}
			wg.Wait()
		}
		if len(dirs) == 0 {
			return
		}
		changes := make([]Change, 0, len(dirs))
		for d := range dirs {
			changes = append(changes, Change{Path: d})
		}
		_, _ = ld.applyLocal(changes)
	}()
}

// WaitBackground waits for work left running by earlier calls (the tag
// pass after a first index).
func (ld *Laserdot) WaitBackground() { ld.background.Wait() }

// reloadWith rebuilds the in-memory store from the other roots in the
// database plus a root just written (its entries are in memory already, so
// they are not read back).
func (ld *Laserdot) reloadWith(root string, fresh []index.RawEntry) error {
	if len(fresh) == 0 {
		return ld.reload()
	}
	t := time.Now()
	raw, err := ld.db.LoadExcept(root)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		raw = fresh // the only root: no copy
	} else {
		off := len(raw)
		raw = slices.Grow(raw, len(fresh)) // one allocation, not doubling
		for _, r := range fresh {
			if r.Parent >= 0 {
				r.Parent += off
			}
			raw = append(raw, r)
		}
	}
	s := index.Build(raw)
	ld.mu.Lock()
	ld.store = s
	ld.loadMS = uint64(time.Since(t).Milliseconds())
	ld.mu.Unlock()
	return nil
}
