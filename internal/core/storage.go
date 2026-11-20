package core

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// Storage (spec §17.3, v1.24): what fills the disk, from the index alone —
// no extra disk reads. A folder's size is the sum of the files under it.

// Packages (apps, libraries, .git folders) are one entry in the index, with
// nothing inside, so the index alone would give an app 0 bytes (the owner saw
// /Applications at 25 KB). Their size is measured from the disk when Storage
// asks, outside the index lock, in parallel, and remembered until the
// package's own time changes. A request spends at most StorageBudget on it;
// what is not measured by then comes back marked Partial ("at least"), and
// the next request goes on from the cache.

// StorageBudget: the longest one Storage request measures packages.
var StorageBudget = 8 * time.Second

// StorageItem is one entry of a folder, with its total size.
type StorageItem struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Bytes uint64 `json:"bytes"`
	// Files: files outside packages; Apps and Packages count those (an app
	// is one app, not its thousands of files).
	Files    uint64 `json:"files"`
	Apps     uint64 `json:"apps"`
	Packages uint64 `json:"packages"`
	// Partial: some package inside is not measured yet (Bytes is at least).
	Partial bool `json:"partial,omitempty"`
}

// StorageReport is a folder's contents, largest first.
type StorageReport struct {
	Path     string        `json:"path"`
	Bytes    uint64        `json:"bytes"`
	Files    uint64        `json:"files"`
	Apps     uint64        `json:"apps"`
	Packages uint64        `json:"packages"`
	Partial  bool          `json:"partial,omitempty"`
	Items    []StorageItem `json:"items"`
}

type pkgRef struct {
	path    string
	modTime int64
	item    int
}

// packageSizes remembers measured packages (path → size at a modification time).
type packageSizes struct {
	mu   sync.Mutex
	size map[string]pkgSize
}

type pkgSize struct {
	modTime int64
	bytes   uint64
}

func (c *packageSizes) get(path string, modTime int64) (uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.size[path]
	return v.bytes, ok && v.modTime == modTime
}

func (c *packageSizes) put(path string, modTime int64, bytes uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.size == nil {
		c.size = map[string]pkgSize{}
	}
	c.size[path] = pkgSize{modTime, bytes}
}

// measurePackage sums the files inside a package (false: the deadline came
// first).
func measurePackage(path string, deadline time.Time) (uint64, bool) {
	var total uint64
	stopped := false
	filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable: skipped
		}
		if time.Now().After(deadline) {
			stopped = true
			return filepath.SkipAll
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += uint64(info.Size())
			}
		}
		return nil
	})
	return total, !stopped
}

// subtree sums the files under dir, and lists the packages in it to measure.
func subtree(s *index.Store, dir uint32, opts scan.Options, item int, pkgs *[]pkgRef, apps, packages *uint64) (bytes, files uint64) {
	it := s.Within(dir).Iterator()
	for it.HasNext() {
		id := it.Next()
		if !s.IsAlive(id) {
			continue
		}
		if !s.IsDir(id) {
			bytes += s.Size(id)
			files++
			continue
		}
		if p := s.Path(id); id != dir && !s.IsSymlink(id) && scan.IsOpaque(p, opts) {
			*pkgs = append(*pkgs, pkgRef{p, s.Modified(id), item})
			if strings.EqualFold(filepath.Ext(p), ".app") {
				*apps++
			} else {
				*packages++
			}
		}
	}
	return bytes, files
}

// Storage lists path's entries (or, for "", the indexed folders) by size,
// at most limit of them.
func (ld *Laserdot) Storage(path string, limit int) (StorageReport, error) {
	if limit <= 0 {
		limit = 50
	}
	ld.mu.RLock()
	s, opts := ld.store, scan.DefaultOptions()
	if path != "" {
		if p, err := canonical(path); err == nil {
			path = p
		}
		if d := ld.driveFor(path); d != nil {
			s, opts = d.store, d.scan
		}
	}
	var ids []uint32
	rep := StorageReport{Path: path, Items: []StorageItem{}}
	if path == "" {
		ids = s.RootIDs()
	} else {
		dir, ok := s.Resolve(path)
		if !ok || !s.IsDir(dir) {
			ld.mu.RUnlock()
			return rep, fmt.Errorf("%s is not an indexed folder", path)
		}
		ids = s.Children(dir)
	}
	var pkgs []pkgRef
	for i, id := range ids {
		it := StorageItem{Path: s.Path(id), Name: s.Name(id), IsDir: s.IsDir(id)}
		switch {
		case it.IsDir && !s.IsSymlink(id) && scan.IsOpaque(it.Path, opts):
			// A package listed itself: measured whole.
			pkgs = append(pkgs, pkgRef{it.Path, s.Modified(id), i})
			if strings.EqualFold(filepath.Ext(it.Path), ".app") {
				it.Apps = 1
			} else {
				it.Packages = 1
			}
		case it.IsDir:
			it.Bytes, it.Files = subtree(s, id, opts, i, &pkgs, &it.Apps, &it.Packages)
		default:
			it.Bytes, it.Files = s.Size(id), 1
		}
		rep.Items = append(rep.Items, it)
	}
	ld.mu.RUnlock()

	// Packages: from the cache, else measured (no lock held, a few at once,
	// within the budget).
	deadline := time.Now().Add(StorageBudget)
	type measured struct {
		ref   pkgRef
		bytes uint64
		ok    bool
	}
	results := make([]measured, len(pkgs))
	var todo []int
	for i, p := range pkgs {
		if b, ok := ld.pkgSizes.get(p.path, p.modTime); ok {
			results[i] = measured{p, b, true}
		} else {
			todo = append(todo, i)
		}
	}
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < min(4, len(todo)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				p := pkgs[i]
				b, ok := measurePackage(p.path, deadline)
				if ok {
					ld.pkgSizes.put(p.path, p.modTime, b)
				}
				results[i] = measured{p, b, ok}
			}
		}()
	}
	for _, i := range todo {
		next <- i
	}
	close(next)
	wg.Wait()
	for _, m := range results {
		it := &rep.Items[m.ref.item]
		it.Bytes += m.bytes
		if !m.ok {
			it.Partial = true
		}
	}
	for _, it := range rep.Items {
		rep.Bytes += it.Bytes
		rep.Files += it.Files
		rep.Apps += it.Apps
		rep.Packages += it.Packages
		rep.Partial = rep.Partial || it.Partial
	}
	sort.Slice(rep.Items, func(i, j int) bool {
		if rep.Items[i].Bytes != rep.Items[j].Bytes {
			return rep.Items[i].Bytes > rep.Items[j].Bytes
		}
		return rep.Items[i].Name < rep.Items[j].Name
	})
	if len(rep.Items) > limit {
		rep.Items = rep.Items[:limit]
	}
	return rep, nil
}
