// Package core is laserdot's public API (test Seam 1): open an index,
// index folders, keep it current, search. The daemon, the CLI and the tests
// use only this.
package core

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
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
	opens  openHistory
	dbPath string
	wmu    sync.Mutex
	db     *index.Db
	mu     sync.RWMutex
	store  *index.Store
	loadMS uint64
	report OpenReport
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
	return opts
}

// Index indexes a folder. A new folder is walked in bulk; a folder that is
// already indexed (or inside one) is refreshed in place, so renames are
// tracked and unchanged files are not touched.
func (ld *Laserdot) Index(root string) (IndexStats, error) {
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
	st, err := index.IndexRoot(ld.db, root, ld.ScanOptions(), index.BulkOptions{})
	if err != nil {
		return st, err
	}
	if id, ok := currentEventID(); ok {
		if err := ld.db.SetEventID(st.Root, id); err != nil {
			return st, err
		}
	}
	return st, ld.reload()
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

// CheckPending re-examines files that were still being written; any that
// are now stable get probed (§2.6.3).
func (ld *Laserdot) CheckPending() (UpdateStats, error) {
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
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	if opts.Page.Sort == index.Relevance {
		opts.Page.Boost = ld.openBoosts(ld.store)
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
		sub.Page.Boost = nil // the open history is this Mac's (ids of its store)
		if n == 0 {
			sub.Page.Boost = opts.Page.Boost
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
