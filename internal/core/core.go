// Package core is laserdot's public API (test Seam 1): open an index,
// index folders, keep it current, search. The daemon, the CLI and the tests
// use only this.
package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	dbPath string
	wmu    sync.Mutex
	db     *index.Db
	mu     sync.RWMutex
	store  *index.Store
	loadMS uint64
	report OpenReport
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
	ld := &Laserdot{dbPath: dbPath, db: db, report: OpenReport{Reindexed: []string{}, Purged: []string{}}}
	if rep.Quarantined != "" {
		ld.report.Quarantined = &rep.Quarantined
	}
	if ld.report.Purged, err = db.PurgeOffline(now.Add(-PurgeAfter).Unix()); err != nil {
		db.Close()
		return nil, err
	}
	if err := ld.reload(); err != nil {
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

// Close closes the database.
func (ld *Laserdot) Close() error { return ld.db.Close() }

// DBPath is the database file.
func (ld *Laserdot) DBPath() string { return ld.dbPath }

// OpenReport is what Open had to do.
func (ld *Laserdot) OpenReport() OpenReport { return ld.report }

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
	for _, suffix := range []string{"", "-wal", "-shm", "-journal", ".roots", ".snapshot", ".snapshot.tmp"} {
		opts.ExcludedPaths = append(opts.ExcludedPaths, filepath.Join(dir, base+suffix))
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

// ApplyChanges applies "something changed under this path" signals.
func (ld *Laserdot) ApplyChanges(changes []Change) (UpdateStats, error) {
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
// folder is rescanned (recursively) and diffed in place.
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
	return ld.ApplyChanges(changes)
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
	return Stats{
		Entries: s.LiveCount(), Roots: s.Roots(), MemoryBytes: s.MemoryBytes(),
		LoadMS: ld.loadMS, Overlay: s.OverlaySize(), Drives: []string{},
	}
}

// Search runs query against the index.
func (ld *Laserdot) Search(q string, opts SearchOptions) (*SearchResults, error) {
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	return SearchStore(ld.store, q, opts)
}

// SearchStore runs a query against any store (also used by benchmarks).
func SearchStore(s *index.Store, q string, opts SearchOptions) (*SearchResults, error) {
	t := time.Now()
	parsed, err := query.Parse(q)
	if err != nil {
		return nil, err
	}
	res := &SearchResults{Hits: []Hit{}, Warnings: parsed.Warnings}
	if res.Warnings == nil {
		res.Warnings = []query.Warning{}
	}
	if parsed.IsEmpty() {
		res.ElapsedUS = uint64(time.Since(t).Microseconds())
		return res, nil
	}
	exact, err := query.Compile(parsed, false)
	if err != nil {
		return nil, err
	}
	exactHits, err := s.Execute(exact)
	if err != nil {
		return nil, err
	}
	ranked := s.Rank(exactHits, exact, opts.Page, 0)
	res.Total = exactHits.GetCardinality()
	typoIDs := roaring.New()
	if opts.TypoFallback && res.Total < TypoFallbackBelow && query.HasTypoCandidates(parsed) {
		typo, err := query.Compile(parsed, true)
		if err != nil {
			return nil, err
		}
		hits, err := s.Execute(typo)
		if err != nil {
			return nil, err
		}
		extra := roaring.AndNot(hits, exactHits)
		if !extra.IsEmpty() {
			res.TypoFallback = true
			res.Total += extra.GetCardinality()
			// Exact matches fill the page first; typo matches follow.
			p := opts.Page
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
	for _, r := range ranked {
		res.Hits = append(res.Hits, hitOf(s, r.ID, typoIDs.Contains(r.ID)))
	}
	res.ElapsedUS = uint64(time.Since(t).Microseconds())
	return res, nil
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
