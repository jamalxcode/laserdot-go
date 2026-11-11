package core

import (
	"fmt"
	"sort"

	"github.com/jamalxcode/laserdot-go/internal/index"
)

// Storage (spec §17.3, v1.24): what fills the disk, from the index alone —
// no extra disk reads. A folder's size is the sum of the files under it.

// StorageItem is one entry of a folder, with its total size.
type StorageItem struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Bytes uint64 `json:"bytes"`
	Files uint64 `json:"files"`
}

// StorageReport is a folder's contents, largest first.
type StorageReport struct {
	Path  string        `json:"path"`
	Bytes uint64        `json:"bytes"`
	Files uint64        `json:"files"`
	Items []StorageItem `json:"items"`
}

// subtree sums the files under dir.
func subtree(s *index.Store, dir uint32) (bytes, files uint64) {
	it := s.Within(dir).Iterator()
	for it.HasNext() {
		id := it.Next()
		if s.IsAlive(id) && !s.IsDir(id) {
			bytes += s.Size(id)
			files++
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
	defer ld.mu.RUnlock()
	s := ld.store
	if path != "" {
		if p, err := canonical(path); err == nil {
			path = p
		}
		if d := ld.driveFor(path); d != nil {
			s = d.store
		}
	}
	var ids []uint32
	rep := StorageReport{Path: path, Items: []StorageItem{}}
	if path == "" {
		ids = s.RootIDs()
	} else {
		dir, ok := s.Resolve(path)
		if !ok || !s.IsDir(dir) {
			return rep, fmt.Errorf("%s is not an indexed folder", path)
		}
		ids = s.Children(dir)
	}
	for _, id := range ids {
		it := StorageItem{Path: s.Path(id), Name: s.Name(id), IsDir: s.IsDir(id)}
		if it.IsDir {
			it.Bytes, it.Files = subtree(s, id)
		} else {
			it.Bytes, it.Files = s.Size(id), 1
		}
		rep.Bytes += it.Bytes
		rep.Files += it.Files
		rep.Items = append(rep.Items, it)
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
