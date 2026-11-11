package core

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"sync"
)

// Excluded folders (spec §13.6, v1.23): folders the user never wants
// indexed, e.g. a private folder inside the home folder. They are kept in
// this Mac's index settings; excluding one removes what was indexed there.

const exclusionsSetting = "excluded_paths"

type exclusionList struct {
	mu     sync.Mutex
	loaded bool
	paths  []string
}

func (ld *Laserdot) loadExclusions() []string {
	e := &ld.exclusions
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.loaded {
		e.loaded = true
		// No wmu: ScanOptions (and so this) runs while Index holds it, and
		// reading a setting needs no writer lock.
		raw, err := ld.db.Setting(exclusionsSetting)
		if err == nil && raw != "" {
			_ = json.Unmarshal([]byte(raw), &e.paths)
		}
	}
	return slices.Clone(e.paths)
}

func (ld *Laserdot) saveExclusions(paths []string) error {
	e := &ld.exclusions
	e.mu.Lock()
	e.loaded, e.paths = true, paths
	e.mu.Unlock()
	data, _ := json.Marshal(paths)
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	return ld.db.SetSetting(exclusionsSetting, string(data))
}

// Exclusions lists the excluded folders.
func (ld *Laserdot) Exclusions() []string {
	out := ld.loadExclusions()
	if out == nil {
		out = []string{}
	}
	return out
}

// Exclude stops indexing path and removes what was indexed under it. An
// excluded indexed folder is forgotten.
func (ld *Laserdot) Exclude(path string) ([]string, error) {
	path, err := canonical(path)
	if err != nil {
		return nil, err
	}
	paths := ld.loadExclusions()
	if !slices.Contains(paths, path) {
		paths = append(paths, path)
		slices.Sort(paths)
	}
	if err := ld.saveExclusions(paths); err != nil {
		return nil, err
	}
	ld.wmu.Lock()
	roots, err := ld.db.Roots()
	ld.wmu.Unlock()
	if err != nil {
		return nil, err
	}
	for _, r := range roots {
		switch {
		case r.Path == path || within(r.Path, path):
			if _, err := ld.Forget(r.Path); err != nil {
				return nil, err
			}
		case within(path, r.Path):
			if _, err := ld.ApplyChanges([]Change{{Path: filepath.Dir(path)}}); err != nil {
				return nil, err
			}
		}
	}
	return paths, nil
}

// Include indexes path again (if it is inside an indexed folder).
func (ld *Laserdot) Include(path string) ([]string, error) {
	if p, err := canonical(path); err == nil {
		path = p
	}
	paths := slices.DeleteFunc(ld.loadExclusions(), func(p string) bool { return p == path })
	if err := ld.saveExclusions(paths); err != nil {
		return nil, err
	}
	if _, err := ld.ApplyChanges([]Change{{Path: filepath.Dir(path)}}); err != nil {
		return nil, err
	}
	return paths, nil
}
