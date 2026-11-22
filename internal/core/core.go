// Package core is laserdot's public API (test Seam 1): open an index,
// index folders, keep it current, search. The daemon, the CLI and the tests
// use only this.
package core

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/crash"

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
	// Inside: a zip matched by the names of files inside it (those, up to
	// a few, as paths inside the zip).
	Inside []string `json:"inside,omitempty"`
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
	// streams: drive indexes resuming in the background (stopped, not
	// waited for, on Close).
	streams sync.WaitGroup
	// demand: folders recent searches were about (drive indexing order).
	demand demand
	// pkgSizes: packages' measured sizes (the Storage window).
	pkgSizes packageSizes
	// releasedMounts: drives being let go (mount → true); see release.
	releasedMounts sync.Map
	// snapDirtySince, snapOverlay: CheckpointWhenQuiet's state (the
	// daemon's checkpoint loop only).
	snapDirtySince time.Time
	snapOverlay    uint64
	// paused: background work waits (an update being installed, battery,
	// heat); drive indexing hands out no new folders meanwhile.
	paused atomic.Bool
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
	content *index.ContentIndex
	// archives: the names inside this Mac's zip files, in their own file.
	archives        *index.ArchiveIndex
	contentSettings ContentSettings
	// Attached drive indexes (§9), their mount folder, and passphrases
	// entered this session (by drive id). Guarded by mu.
	drives  []*drive
	volumes VolumeConfig
	keys    map[string]string
	// creating: drives being indexed for the first time (not attached or
	// indexed twice meanwhile).
	creating map[string]bool
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
		volumes: DefaultVolumeConfig(), keys: map[string]string{}, creating: map[string]bool{}}
	if ld.content, err = index.OpenContent(contentPath(dbPath)); err != nil {
		db.Close()
		return nil, err
	}
	if ld.archives, err = index.OpenArchives(archivesPath(dbPath)); err != nil {
		ld.content.Close()
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
	// Off the start: on a Mac with thousands of folders the one-time
	// cleanup could hold searches up for seconds.
	ld.background.Add(1)
	go func() {
		defer ld.background.Done()
		defer crash.Guard("clearing system files from the index")
		ld.dropSystemJunk()
	}()
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
	ld.streams.Wait()
	ld.content.Close()
	ld.archives.Close()
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
