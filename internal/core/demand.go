package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Search demand (Phase B): while a drive is being indexed for the first
// time, the folders the user is searching in are read before the rest. A
// search records the folders it is about (its in: folders, and the folders
// of its results on a drive); the streaming indexer asks searchPriority
// for each waiting folder. Kept in memory only, for ten minutes.

const (
	demandKeep = 10 * time.Minute
	demandMax  = 32
)

type demand struct {
	mu      sync.Mutex
	folders []demandFolder
	version uint64
}

type demandFolder struct {
	path string
	at   time.Time
}

func (d *demand) add(paths []string) {
	if len(paths) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	for _, p := range paths {
		d.folders = slices.DeleteFunc(d.folders, func(f demandFolder) bool { return f.path == p || now.Sub(f.at) > demandKeep })
		d.folders = append(d.folders, demandFolder{p, now})
	}
	if over := len(d.folders) - demandMax; over > 0 {
		d.folders = d.folders[over:]
	}
	d.version++
}

// priority: a folder inside one searched in comes first; the folders on
// the way to it next (they must be read to reach it).
func (d *demand) priority(path string) float64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	best := 0.0
	now := time.Now()
	for _, f := range d.folders {
		if now.Sub(f.at) > demandKeep {
			continue
		}
		switch {
		case within(path, f.path):
			best = max(best, 20)
		case within(f.path, path):
			best = max(best, 10)
		}
	}
	return best
}

func (d *demand) currentVersion() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.version
}

func (ld *Laserdot) searchPriority(path string) float64 { return ld.demand.priority(path) }

// noteDemand records what a search was about, when a drive is being indexed.
func (ld *Laserdot) noteDemand(q string, r *SearchResults) {
	ld.mu.RLock()
	var indexing []string
	for _, d := range ld.drives {
		if d.indexing.Load() {
			indexing = append(indexing, d.mount)
		}
	}
	ld.mu.RUnlock()
	if len(indexing) == 0 {
		return
	}
	onDrive := func(p string) bool {
		for _, m := range indexing {
			if within(p, m) {
				return true
			}
		}
		return false
	}
	var paths []string
	for _, f := range strings.Fields(q) {
		v, ok := strings.CutPrefix(f, "in:")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		if strings.HasPrefix(v, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				v = filepath.Join(home, v[2:])
			}
		}
		if filepath.IsAbs(v) && onDrive(filepath.Clean(v)) {
			paths = append(paths, filepath.Clean(v))
		}
	}
	if r != nil {
		for i, h := range r.Hits {
			if i == 10 {
				break
			}
			if h.Volume != nil && onDrive(h.Path) {
				paths = append(paths, filepath.Dir(h.Path))
			}
		}
	}
	ld.demand.add(paths)
}
