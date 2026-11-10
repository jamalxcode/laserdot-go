// Package core is laserdot's public API (test Seam 1): open an index,
// index folders, search. The daemon, the CLI and the tests use only this.
package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	// Media facts (G2); null until then.
	Media any `json:"media"`
	// Volume: the removable drive it is on (nil = this Mac's index).
	Volume *string `json:"volume"`
	// Content: matched by its contents, not its name (G4).
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
	Entries      uint64   `json:"entries"`
	Roots        []string `json:"roots"`
	MemoryBytes  uint64   `json:"memory_bytes"`
	MappedBytes  uint64   `json:"mapped_bytes"`
	LoadMS       uint64   `json:"load_ms"`
	FromSnapshot bool     `json:"from_snapshot"`
	Overlay      uint64   `json:"overlay"`
	Drives       []string `json:"drives"`
}

// IndexStats reports an index run.
type IndexStats struct {
	Root        string `json:"root"`
	Files       uint64 `json:"files"`
	Folders     uint64 `json:"folders"`
	Probed      uint64 `json:"probed"`
	Reused      uint64 `json:"reused"`
	ProbeFailed uint64 `json:"probe_failed"`
	Dataless    uint64 `json:"dataless"`
	ScanErrors  uint64 `json:"scan_errors"`
	ScanMS      uint64 `json:"scan_ms"`
	ProbeMS     uint64 `json:"probe_ms"`
	WriteMS     uint64 `json:"write_ms"`
	// Incremental: a refresh of an indexed folder rather than a first index.
	Incremental bool   `json:"incremental"`
	Added       uint64 `json:"added"`
	Removed     uint64 `json:"removed"`
	Renamed     uint64 `json:"renamed"`
	Modified    uint64 `json:"modified"`
	Pending     uint64 `json:"pending"`
}

// UpdateStats reports a refresh.
type UpdateStats struct {
	DirsScanned  uint64   `json:"dirs_scanned"`
	Added        uint64   `json:"added"`
	Removed      uint64   `json:"removed"`
	Renamed      uint64   `json:"renamed"`
	Modified     uint64   `json:"modified"`
	Probed       uint64   `json:"probed"`
	ProbeFailed  uint64   `json:"probe_failed"`
	Pending      uint64   `json:"pending"`
	Reused       uint64   `json:"reused"`
	OfflineRoots []string `json:"offline_roots"`
	MS           uint64   `json:"ms"`
}

// OpenReport says what Open did beyond opening.
type OpenReport struct {
	// Quarantined: the database failed its integrity check and was moved.
	Quarantined *string `json:"quarantined"`
	// Reindexed: folders re-indexed after a quarantine.
	Reindexed []string `json:"reindexed"`
	// Purged: offline folders purged (G3).
	Purged []string `json:"purged"`
}

// Laserdot is an open index. Searches run concurrently with each other and
// with indexing: writers build a new store and swap it in.
type Laserdot struct {
	dbPath string
	mu     sync.Mutex // serializes writers (the database and the swap)
	db     *index.Db
	store  atomic.Pointer[index.Store]
	loadMS atomic.Uint64
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
// database is quarantined and its folders re-indexed (§2.6.4).
func Open(dbPath string) (*Laserdot, error) {
	db, rep, err := index.Open(dbPath)
	if err != nil {
		return nil, err
	}
	ld := &Laserdot{dbPath: dbPath, db: db, report: OpenReport{Reindexed: []string{}, Purged: []string{}}}
	if rep.Quarantined != "" {
		ld.report.Quarantined = &rep.Quarantined
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

func (ld *Laserdot) reload() error {
	t := time.Now()
	raw, err := ld.db.Load()
	if err != nil {
		return err
	}
	ld.store.Store(index.Build(raw))
	ld.loadMS.Store(uint64(time.Since(t).Milliseconds()))
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
	return path == root || strings.HasPrefix(path, strings.TrimSuffix(root, string(os.PathSeparator))+string(os.PathSeparator))
}

// Index indexes a folder. A folder that is already indexed (or inside one)
// is re-walked as a whole (G1; G3 diffs in place).
func (ld *Laserdot) Index(root string) (IndexStats, error) {
	root, err := canonical(root)
	if err != nil {
		return IndexStats{}, err
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return IndexStats{}, fmt.Errorf("%s is not a folder", root)
	}
	ld.mu.Lock()
	defer ld.mu.Unlock()
	roots, err := ld.db.Roots()
	if err != nil {
		return IndexStats{}, err
	}
	target, incremental := root, false
	for _, r := range roots {
		if within(root, r.Path) {
			target, incremental = r.Path, true
			break
		}
	}
	before := ld.store.Load().LiveCount()
	st, err := ld.indexRoot(target)
	if err != nil {
		return st, err
	}
	if err := ld.reload(); err != nil {
		return st, err
	}
	st.Root = root
	if incremental {
		st.Incremental = true
		after := ld.store.Load().LiveCount()
		if after > before {
			st.Added = after - before
		} else {
			st.Removed = before - after
		}
	}
	return st, nil
}

// indexRoot walks root and replaces its rows. Caller holds mu.
func (ld *Laserdot) indexRoot(root string) (IndexStats, error) {
	st := IndexStats{Root: root}
	t := time.Now()
	entries, errs, err := scan.Walk(root, ld.scanOptions())
	if err != nil {
		return st, err
	}
	st.ScanMS = uint64(time.Since(t).Milliseconds())
	st.ScanErrors = uint64(len(errs))
	for _, e := range entries[1:] {
		if e.IsDir {
			st.Folders++
		} else {
			st.Files++
		}
		if e.Dataless {
			st.Dataless++
		}
	}
	t = time.Now()
	if err := ld.db.ReplaceRoot(root, entries); err != nil {
		return st, err
	}
	st.WriteMS = uint64(time.Since(t).Milliseconds())
	return st, nil
}

// scanOptions: the defaults, plus the index's own files (the index never
// indexes itself).
func (ld *Laserdot) scanOptions() scan.Options {
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

// Refresh re-walks every indexed folder (G1: whole folders; G3 diffs).
// Missing folders are kept and reported offline.
func (ld *Laserdot) Refresh() (UpdateStats, error) {
	t := time.Now()
	ld.mu.Lock()
	defer ld.mu.Unlock()
	u := UpdateStats{OfflineRoots: []string{}}
	roots, err := ld.db.Roots()
	if err != nil {
		return u, err
	}
	before := ld.store.Load().LiveCount()
	for _, r := range roots {
		if fi, err := os.Stat(r.Path); err != nil || !fi.IsDir() {
			u.OfflineRoots = append(u.OfflineRoots, r.Path)
			continue
		}
		st, err := ld.indexRoot(r.Path)
		if err != nil {
			return u, err
		}
		u.DirsScanned += st.Folders + 1
	}
	if err := ld.reload(); err != nil {
		return u, err
	}
	if after := ld.store.Load().LiveCount(); after > before {
		u.Added = after - before
	} else {
		u.Removed = before - after
	}
	u.MS = uint64(time.Since(t).Milliseconds())
	return u, nil
}

// Forget removes an indexed folder. False when it was not indexed.
func (ld *Laserdot) Forget(root string) (bool, error) {
	if c, err := canonical(root); err == nil {
		root = c
	} else {
		root = filepath.Clean(root)
	}
	ld.mu.Lock()
	defer ld.mu.Unlock()
	ok, err := ld.db.RemoveRoot(root)
	if err != nil {
		return false, err
	}
	return ok, ld.reload()
}

// Roots are the indexed folders with their state.
func (ld *Laserdot) Roots() ([]index.RootInfo, error) {
	ld.mu.Lock()
	defer ld.mu.Unlock()
	r, err := ld.db.Roots()
	if r == nil {
		r = []index.RootInfo{}
	}
	return r, err
}

// Stats describes the loaded index.
func (ld *Laserdot) Stats() Stats {
	s := ld.store.Load()
	return Stats{
		Entries: s.LiveCount(), Roots: s.Roots(), MemoryBytes: s.MemoryBytes(),
		LoadMS: ld.loadMS.Load(), Drives: []string{},
	}
}

// Search runs query against the index.
func (ld *Laserdot) Search(q string, opts SearchOptions) (*SearchResults, error) {
	return SearchStore(ld.store.Load(), q, opts)
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
