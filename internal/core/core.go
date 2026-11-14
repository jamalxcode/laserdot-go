// Package core is laserdot's public API (test Seam 1): open an index,
// index folders, keep it current, search. The daemon, the CLI and the tests
// use only this.
package core

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/query"
	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// TypoFallbackBelow: with fewer exact matches, typo alternatives are tried
// (§2.2.3).
const TypoFallbackBelow = 5

// typoPenalty ranks typo-only matches after every exact match.
const typoPenalty = 1 << 12

// PurgeAfter: offline folders unverified this long are dropped (§2.6.2).
const PurgeAfter = 30 * 24 * time.Hour

// Re-exported types.
type (
	IndexStats  = index.IndexStats
	UpdateStats = index.UpdateStats
	RootInfo    = index.RootInfo
	Change      = scan.Change
)

// SearchOptions for Search.
type SearchOptions struct {
	Page index.Page
	// TypoFallback: try typo alternatives when exact matching finds few
	// results.
	TypoFallback bool
}

// DefaultSearchOptions: relevance, first 100, typo fallback on.
var DefaultSearchOptions = SearchOptions{Page: index.DefaultPage, TypoFallback: true}

// Hit is one result. JSON field names are the daemon protocol's.
type Hit struct {
	Path     string  `json:"path"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	IsDir    bool    `json:"is_dir"`
	Size     uint64  `json:"size"`
	Modified int64   `json:"modified"` // Unix seconds
	Created  int64   `json:"created"`
	Width    *uint32 `json:"width"`
	Height   *uint32 `json:"height"`
	// Typo: matched only through a typo alternative.
	Typo bool `json:"typo"`
	// Media: duration, codecs, audio tracks… when the file has them.
	Media *index.MediaSummary `json:"media"`
	// Volume: the removable drive it is on (nil = this Mac's index).
	Volume *string `json:"volume"`
	// Content: matched by its contents, not its name (§2.4.2).
	Content bool `json:"content"`
	// Opened: when it was last opened from Laserdot (Unix seconds; absent
	// when never). The open history is this Mac's only.
	Opened int64 `json:"opened,omitempty"`
}

// SearchResults is one page of results.
type SearchResults struct {
	// Total matches (all pages).
	Total uint64 `json:"total"`
	Hits  []Hit  `json:"hits"`
	// TypoFallback: typo alternatives found (some of) these results.
	TypoFallback bool            `json:"typo_fallback"`
	Warnings     []query.Warning `json:"warnings"`
	ElapsedUS    uint64          `json:"elapsed_us"`
}

// Stats describes the loaded index.
type Stats struct {
	Entries     uint64   `json:"entries"`
	Roots       []string `json:"roots"`
	MemoryBytes uint64   `json:"memory_bytes"`
	// MappedBytes: memory served from the mapped snapshot (§12.7).
	MappedBytes  uint64 `json:"mapped_bytes"`
	LoadMS       uint64 `json:"load_ms"`
	FromSnapshot bool   `json:"from_snapshot"`
	// Overlay: changes applied in memory since the last rebuild.
	Overlay uint64   `json:"overlay"`
	Drives  []string `json:"drives"`
}

// OpenReport says what Open did beyond opening.
type OpenReport struct {
	// Quarantined: the database failed its integrity check and was moved.
	Quarantined *string `json:"quarantined"`
	// Reindexed: folders re-indexed after a quarantine.
	Reindexed []string `json:"reindexed"`
	// Purged: offline folders purged after 30 days unverified.
	Purged []string `json:"purged"`
}

// Laserdot is an open index.
//
// Locking: mu guards the in-memory store; searches take it shared, writers
// exclusively while they change it. wmu serializes writers (the database
// and the store change together).
type Laserdot struct {
	// background: work a call starts and leaves running (the tag pass
	// after a first index); WaitBackground waits for it.
	background sync.WaitGroup
	// fixing: folders being listed again because a result there was gone.
	fixing sync.Map
	// probing: the background header pass is running.
	probing    atomic.Bool
	opens      openHistory
	exclusions exclusionList
	progress   atomic.Pointer[IndexProgress]
	// details: the background header pass (probeLater), when running.
	details atomic.Pointer[IndexProgress]
	// dupes: the duplicate scan running now.
	dupes atomic.Pointer[DupeProgress]
	// dupeStop: StopDuplicates asked the running scan to stop early.
	dupeStop atomic.Bool
	// dupeCurrent: the file the scan started reading last.
	dupeCurrent atomic.Pointer[string]
	dbPath      string
	wmu         sync.Mutex
	db          *index.Db
	mu          sync.RWMutex
	store       *index.Store
	loadMS      uint64
	report      OpenReport
	// content: file contents (§2.4), in its own file next to the index.
	content         *index.ContentIndex
	contentSettings ContentSettings
	// Attached drive indexes (§9), their mount folder, and passphrases
	// entered this session (by drive id). Guarded by mu.
	drives  []*drive
	volumes VolumeConfig
	keys    map[string]string
}

// DefaultDBPath is $LASERDOT_DB, else
// ~/Library/Application Support/laserdot/index.db (macOS) or
// $XDG_DATA_HOME/laserdot/index.db (~/.local/share/laserdot/index.db).
func DefaultDBPath() string {
	if p := os.Getenv("LASERDOT_DB"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".local", "share")
	if runtime.GOOS == "darwin" {
		base = filepath.Join(home, "Library", "Application Support")
	} else if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		base = x
	}
	return filepath.Join(base, "laserdot", "index.db")
}

// Open opens (or creates) the index and loads it into memory. A corrupt
// database is quarantined and its folders re-indexed (§2.6.4); folders
// offline for 30 days are purged.
func Open(dbPath string) (*Laserdot, error) { return OpenAt(dbPath, time.Now()) }

// OpenAt is Open with an explicit "now" (for purge tests).
func OpenAt(dbPath string, now time.Time) (*Laserdot, error) {
	db, rep, err := index.Open(dbPath)
	if err != nil {
		return nil, err
	}
	ld := &Laserdot{dbPath: dbPath, db: db, report: OpenReport{Reindexed: []string{}, Purged: []string{}},
		volumes: DefaultVolumeConfig(), keys: map[string]string{}}
	if ld.content, err = index.OpenContent(contentPath(dbPath)); err != nil {
		db.Close()
		return nil, err
	}
	ld.loadContentSettings()
	if rep.Quarantined != "" {
		ld.report.Quarantined = &rep.Quarantined
	}
	if ld.report.Purged, err = db.PurgeOffline(now.Add(-PurgeAfter).Unix()); err != nil {
		db.Close()
		return nil, err
	}
	if err := ld.load(); err != nil {
		db.Close()
		return nil, err
	}
	for _, root := range rep.RootsToReindex {
		if fi, err := os.Stat(root); err == nil && fi.IsDir() {
			if _, err := ld.Index(root); err == nil {
				ld.report.Reindexed = append(ld.report.Reindexed, root)
			}
		}
	}
	return ld, nil
}

// Close closes the databases.
func (ld *Laserdot) Close() error {
	ld.background.Wait()
	ld.closeDrives()
	ld.content.Close()
	return ld.db.Close()
}

// DBPath is the database file.
func (ld *Laserdot) DBPath() string { return ld.dbPath }

// OpenReport is what Open had to do.
func (ld *Laserdot) OpenReport() OpenReport { return ld.report }

// load serves the store from the snapshot when it matches the database
// (§2.1.1g), else rebuilds it from SQLite. A damaged snapshot is ignored
// (and replaced at the next checkpoint).
func (ld *Laserdot) load() error {
	t := time.Now()
	if token, err := ld.db.SnapshotToken(); err == nil && token != 0 {
		if s, err := index.LoadSnapshot(index.SnapshotPath(ld.dbPath), token); err == nil && s != nil {
			ld.mu.Lock()
			ld.store = s
			ld.loadMS = uint64(time.Since(t).Milliseconds())
			ld.mu.Unlock()
			return nil
		}
	}
	return ld.reload()
}

// Checkpoint writes a snapshot of this Mac's index if the current one is
// stale, so the next launch maps it instead of rebuilding from SQLite.
// Returns whether one was written.
func (ld *Laserdot) Checkpoint() (bool, error) {
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	if token, err := ld.db.SnapshotToken(); err != nil || token != 0 {
		return false, err
	}
	var b [8]byte
	rand.Read(b[:])
	token := binary.LittleEndian.Uint64(b[:]) | 1
	ld.mu.RLock()
	err := index.WriteSnapshot(ld.store, index.SnapshotPath(ld.dbPath), token)
	ld.mu.RUnlock()
	if err != nil {
		return false, err
	}
	return true, ld.db.SetSnapshotToken(token)
}

// Remap serves this Mac's index from a current snapshot (writing one if
// needed): its columns become file-backed page cache (§12.7).
func (ld *Laserdot) Remap() error {
	ld.mu.RLock()
	done := ld.store.FromSnapshot() && ld.store.OverlaySize() == 0
	ld.mu.RUnlock()
	if done {
		return nil
	}
	if _, err := ld.Checkpoint(); err != nil {
		return err
	}
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	token, err := ld.db.SnapshotToken()
	if err != nil || token == 0 {
		return err
	}
	s, err := index.LoadSnapshot(index.SnapshotPath(ld.dbPath), token)
	if err != nil || s == nil {
		return err
	}
	ld.mu.Lock()
	ld.store = s
	ld.mu.Unlock()
	return nil
}

// reload rebuilds the store from SQLite and swaps it in. Callers hold wmu
// (or are opening).
func (ld *Laserdot) reload() error {
	t := time.Now()
	raw, err := ld.db.Load()
	if err != nil {
		return err
	}
	s := index.Build(raw)
	ld.mu.Lock()
	ld.store = s
	ld.loadMS = uint64(time.Since(t).Milliseconds())
	ld.mu.Unlock()
	return nil
}

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
	for _, b := range []string{base, "content.db"} {
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
		if within(root, r.Path) {
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
			if !ld.store.IsDataless(id) && time.Now().Unix()-ld.store.Modified(id) >= index.StableSecs {
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
	ld.mu.Lock()
	u, err := index.Apply(ld.db, ld.store, changes, ld.ScanOptions())
	compact := err == nil && ld.store.NeedsCompaction()
	ld.mu.Unlock()
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
// folder and every attached drive is rescanned and diffed in place.
func (ld *Laserdot) Refresh() (UpdateStats, error) {
	ld.wmu.Lock()
	roots, err := ld.db.Roots()
	ld.wmu.Unlock()
	if err != nil {
		return UpdateStats{OfflineRoots: []string{}}, err
	}
	var changes []Change
	for _, r := range roots {
		changes = append(changes, Change{Path: r.Path, Recursive: true})
	}
	u, err := ld.applyLocal(changes)
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
		du, err := ld.RefreshDrive(m)
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
	type folder struct {
		path string
		mod  int64
	}
	var folders []folder
	ld.mu.RLock()
	it := ld.store.Live().Iterator()
	for it.HasNext() {
		id := it.Next()
		if ld.store.IsDir(id) && !ld.store.IsSymlink(id) {
			folders = append(folders, folder{ld.store.Path(id), ld.store.Modified(id)})
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

// Search runs query against the index (names, then contents).
func (ld *Laserdot) Search(q string, opts SearchOptions) (*SearchResults, error) {
	r, err := ld.search(q, opts)
	if err == nil && r != nil {
		ld.dropMissing(r)
		ld.addOpened(r)
	}
	return r, err
}

// dropMissing leaves out results whose file is gone (deleted while the
// change was missed, say) and has their folders listed again in the
// background, so the index forgets them too. Only this Mac's results are
// checked (a drive could be a slow network volume); a file macOS will not
// let Laserdot see is kept.
func (ld *Laserdot) dropMissing(r *SearchResults) {
	var missing []int
	for i, h := range r.Hits {
		if h.Volume == nil {
			if _, err := os.Lstat(h.Path); errors.Is(err, fs.ErrNotExist) {
				missing = append(missing, i)
			}
		}
	}
	if len(missing) == 0 {
		return
	}
	// An indexed folder that is itself missing is offline (its entries stay
	// searchable for 30 days, §10.3): only files whose indexed folder is
	// still there count as deleted.
	roots, _ := ld.db.Roots()
	rootOf := func(p string) string {
		best := ""
		for _, rt := range roots {
			if within(p, rt.Path) && len(rt.Path) > len(best) {
				best = rt.Path
			}
		}
		return best
	}
	drop := map[int]bool{}
	var gone []string
	for _, i := range missing {
		p := r.Hits[i].Path
		if root := rootOf(p); root != "" {
			if _, err := os.Stat(root); err == nil {
				drop[i] = true
				gone = append(gone, p)
			}
		}
	}
	if len(gone) == 0 {
		return
	}
	kept := r.Hits[:0]
	for i, h := range r.Hits {
		if !drop[i] {
			kept = append(kept, h)
		}
	}
	r.Hits = kept
	r.Total -= min(r.Total, uint64(len(gone)))
	var changes []Change
	for _, p := range gone {
		dir := filepath.Dir(p)
		if _, busy := ld.fixing.LoadOrStore(dir, true); !busy {
			changes = append(changes, Change{Path: dir})
		}
	}
	if len(changes) == 0 {
		return
	}
	ld.background.Add(1)
	go func() {
		defer ld.background.Done()
		_, _ = ld.ApplyChanges(changes)
		for _, c := range changes {
			ld.fixing.Delete(c.Path)
		}
	}()
}

func (ld *Laserdot) search(q string, opts SearchOptions) (*SearchResults, error) {
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	if opts.Page.Sort == index.Relevance {
		opts.Page.Boost = ld.openBoosts(ld.store)
	}
	if opts.Page.Sort == index.ByOpened {
		opts.Page.Opened = ld.openTimes(ld.store)
	}
	if len(ld.drives) == 0 {
		r, _, err := searchStoreKeyed(ld.store, ld.content, q, opts)
		return r, err
	}
	// This Mac's index and every attached drive's, merged into one ranking.
	t := time.Now()
	want := opts.Page.Offset + opts.Page.Limit
	sub := opts
	sub.Page.Offset, sub.Page.Limit = 0, want
	type keyed struct {
		key index.RankKey
		src int
		hit Hit
	}
	var merged []keyed
	out := &SearchResults{Hits: []Hit{}, Warnings: []query.Warning{}}
	var firstErr error
	ok := false
	for n := 0; n <= len(ld.drives); n++ {
		s, content := ld.store, ld.content
		var volume *string
		if n > 0 {
			d := ld.drives[n-1]
			s, content, volume = d.store, nil, &d.name // content search covers this Mac only
		}
		sub.Page.Boost, sub.Page.Opened = nil, nil // the open history is this Mac's (ids of its store)
		if n == 0 {
			sub.Page.Boost, sub.Page.Opened = opts.Page.Boost, opts.Page.Opened
		}
		r, keys, err := searchStoreKeyed(s, content, q, sub)
		if err != nil {
			// e.g. an in: folder only one index has
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ok = true
		out.Total += r.Total
		out.TypoFallback = out.TypoFallback || r.TypoFallback
		if len(out.Warnings) == 0 {
			out.Warnings = r.Warnings
		}
		for i, h := range r.Hits {
			h.Volume = volume
			merged = append(merged, keyed{keys[i], n, h})
		}
	}
	if !ok {
		return nil, firstErr
	}
	slices.SortStableFunc(merged, func(a, b keyed) int {
		switch {
		case a.key.Less(b.key):
			return -1
		case b.key.Less(a.key):
			return 1
		}
		return a.src - b.src
	})
	for i := opts.Page.Offset; i < min(len(merged), want); i++ {
		out.Hits = append(out.Hits, merged[i].hit)
	}
	out.ElapsedUS = uint64(time.Since(t).Microseconds())
	return out, nil
}

// SearchStore runs a query against any store (also used by benchmarks).
func SearchStore(s *index.Store, q string, opts SearchOptions) (*SearchResults, error) {
	r, _, err := searchStoreKeyed(s, nil, q, opts)
	return r, err
}

// contentRankBase: content hits rank after every name hit (§2.4.2), in
// BM25 order.
const contentRankBase = 1 << 60 // above every exact name key (< 2^56), below typo keys (≥ 2^62)

// searchStoreKeyed is a search plus each hit's sort key, for merging
// across indexes. content may be nil (a drive's index has none).
func searchStoreKeyed(s *index.Store, content *index.ContentIndex, q string, opts SearchOptions) (*SearchResults, []index.RankKey, error) {
	t := time.Now()
	parsed, err := query.Parse(q)
	if err != nil {
		return nil, nil, err
	}
	parts, err := splitContent(parsed)
	if err != nil {
		return nil, nil, err
	}
	pq := parts.query
	res := &SearchResults{Hits: []Hit{}, Warnings: pq.Warnings}
	if res.Warnings == nil {
		res.Warnings = []query.Warning{}
	}
	if pq.IsEmpty() && len(parts.filters) == 0 {
		res.ElapsedUS = uint64(time.Since(t).Microseconds())
		return res, nil, nil
	}
	// content: terms: a set every result must (or must not) be in.
	var must *roaring.Bitmap
	mustNot := roaring.New()
	for _, f := range parts.filters {
		set := roaring.New()
		if content != nil {
			paths, err := content.Matching(f.expr)
			if err != nil {
				return nil, nil, err
			}
			set.AddMany(contentIDs(s, paths))
		}
		if f.negated {
			mustNot.Or(set)
		} else if must == nil {
			must = set
		} else {
			must.And(set)
		}
	}
	restrict := func(b *roaring.Bitmap) *roaring.Bitmap {
		if must != nil {
			b.And(must)
		}
		b.AndNot(mustNot)
		return b
	}
	exact, err := query.Compile(pq, false)
	if err != nil {
		return nil, nil, err
	}
	exactHits, err := s.Execute(exact)
	if err != nil {
		return nil, nil, err
	}
	exactHits = restrict(exactHits)
	ranked := s.Rank(exactHits, exact, opts.Page, 0)
	res.Total = exactHits.GetCardinality()
	typoIDs, contentHit := roaring.New(), roaring.New()
	nameHits := exactHits.Clone()
	p := opts.Page
	if opts.TypoFallback && res.Total < TypoFallbackBelow && query.HasTypoCandidates(pq) {
		typo, err := query.Compile(pq, true)
		if err != nil {
			return nil, nil, err
		}
		hits, err := s.Execute(typo)
		if err != nil {
			return nil, nil, err
		}
		extra := roaring.AndNot(restrict(hits), exactHits)
		if !extra.IsEmpty() {
			res.TypoFallback = true
			res.Total += extra.GetCardinality()
			nameHits.Or(extra)
			// Exact matches fill the page first; typo matches follow.
			shown := int(exactHits.GetCardinality())
			remaining := max(p.Offset+p.Limit-shown, 0)
			tp := index.Page{Sort: p.Sort, Reverse: p.Reverse, Offset: max(p.Offset-shown, 0), Limit: min(remaining, p.Limit)}
			for _, r := range s.Rank(extra, typo, tp, typoPenalty) {
				typoIDs.Add(r.ID)
				ranked = append(ranked, r)
			}
			if len(ranked) > p.Limit {
				ranked = ranked[:p.Limit]
			}
		}
	}
	// Plain words also match contents (§2.4.2): entries that satisfy the
	// rest of the query, ranked after every name match.
	if expr, ok := index.FTSQuery(parts.words, true); content != nil && ok {
		rest, err := query.Compile(parts.withoutWords, false)
		if err != nil {
			return nil, nil, err
		}
		allowed, err := s.Execute(rest)
		if err != nil {
			return nil, nil, err
		}
		allowed = roaring.AndNot(restrict(allowed), nameHits)
		paths, err := content.Search(expr, ContentResults)
		if err != nil {
			return nil, nil, err
		}
		var cids []uint32
		for _, id := range contentIDs(s, paths) {
			if allowed.Contains(id) {
				cids = append(cids, id)
			}
		}
		if len(cids) > 0 {
			shown := int(nameHits.GetCardinality())
			offset := max(p.Offset-shown, 0)
			room := min(max(p.Offset+p.Limit-shown, 0), p.Limit)
			res.Total += uint64(len(cids))
			var add []index.Ranked
			if p.Sort == index.Relevance {
				for i := offset; i < min(len(cids), offset+room); i++ {
					add = append(add, index.Ranked{ID: cids[i], Key: index.RankKey{Num: contentRankBase + uint64(i)}})
				}
			} else {
				set := roaring.New()
				set.AddMany(cids)
				add = s.Rank(set, rest, index.Page{Sort: p.Sort, Reverse: p.Reverse, Offset: offset, Limit: room}, 0)
			}
			for _, r := range add {
				contentHit.Add(r.ID)
			}
			ranked = append(ranked, add...)
			if len(ranked) > p.Limit {
				ranked = ranked[:p.Limit]
			}
		}
	}
	keys := make([]index.RankKey, 0, len(ranked))
	for _, r := range ranked {
		h := hitOf(s, r.ID, typoIDs.Contains(r.ID))
		h.Content = contentHit.Contains(r.ID)
		res.Hits = append(res.Hits, h)
		keys = append(keys, r.Key)
	}
	res.ElapsedUS = uint64(time.Since(t).Microseconds())
	return res, keys, nil
}

func hitOf(s *index.Store, id uint32, typo bool) Hit {
	h := Hit{
		Path: s.Path(id), Name: s.Name(id), Kind: s.Kind(id).String(), IsDir: s.IsDir(id),
		Size: s.Size(id), Modified: s.Modified(id), Created: s.Created(id), Typo: typo,
		Media: s.MediaSummary(id),
	}
	if w, ht, ok := s.Dimensions(id); ok {
		h.Width, h.Height = &w, &ht
	}
	return h
}

// IsQueryError reports whether err is a positioned query error (for the
// hint line), returning it.
func IsQueryError(err error) (*query.Error, bool) {
	var e *query.Error
	ok := errors.As(err, &e)
	return e, ok
}
