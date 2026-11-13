package core

import (
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/index"
)

// Open history (spec §6.2): results the user opened rank higher among
// equally good matches, more so when opened often and recently. It stays in
// this Mac's index (a setting), never in a drive's, and never leaves the Mac.

const (
	opensSetting = "opens"
	opensMax     = 1000
)

type openRecord struct {
	Count int   `json:"count"`
	Last  int64 `json:"last"`
}

type openHistory struct {
	mu      sync.Mutex
	loaded  bool
	records map[string]openRecord
}

func (ld *Laserdot) loadOpens() {
	h := &ld.opens
	if h.loaded {
		return
	}
	h.loaded = true
	h.records = map[string]openRecord{}
	// Not under wmu: Search calls this holding mu, and Remap holds wmu
	// while it waits for mu. A read needs no writer lock (sql.DB is safe).
	raw, err := ld.db.Setting(opensSetting)
	if err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &h.records)
	}
}

// RecordOpen notes that the user opened (or revealed) path.
func (ld *Laserdot) RecordOpen(path string) error {
	if p, err := canonical(path); err == nil {
		path = p
	}
	h := &ld.opens
	h.mu.Lock()
	ld.loadOpens()
	r := h.records[path]
	r.Count++
	r.Last = time.Now().Unix()
	h.records[path] = r
	if len(h.records) > opensMax { // forget the least recently opened
		paths := make([]string, 0, len(h.records))
		for p := range h.records {
			paths = append(paths, p)
		}
		sort.Slice(paths, func(i, j int) bool { return h.records[paths[i]].Last < h.records[paths[j]].Last })
		for _, p := range paths[:len(paths)-opensMax] {
			delete(h.records, p)
		}
	}
	data, _ := json.Marshal(h.records)
	h.mu.Unlock()
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	return ld.db.SetSetting(opensSetting, string(data))
}

// ClearOpens forgets the open history.
func (ld *Laserdot) ClearOpens() error {
	h := &ld.opens
	h.mu.Lock()
	h.loaded, h.records = true, map[string]openRecord{}
	h.mu.Unlock()
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	return ld.db.SetSetting(opensSetting, "{}")
}

// openBoosts maps the opened entries of s to a boost (1–255).
func (ld *Laserdot) openBoosts(s *index.Store) map[uint32]uint8 {
	h := &ld.opens
	h.mu.Lock()
	defer h.mu.Unlock()
	ld.loadOpens()
	if len(h.records) == 0 {
		return nil
	}
	now := time.Now().Unix()
	out := make(map[uint32]uint8, len(h.records))
	for p, r := range h.records {
		id, ok := s.Resolve(p)
		if !ok {
			continue
		}
		w := 1
		switch age := now - r.Last; {
		case age < 7*86400:
			w = 4
		case age < 30*86400:
			w = 2
		}
		out[id] = uint8(min(255, r.Count*w))
	}
	return out
}

// RecentOpens: the results opened most recently that still exist, newest
// first (the empty search box shows them).
func (ld *Laserdot) RecentOpens(limit int) []Hit {
	h := &ld.opens
	h.mu.Lock()
	ld.loadOpens()
	type rec struct {
		path string
		last int64
	}
	recs := make([]rec, 0, len(h.records))
	for p, r := range h.records {
		recs = append(recs, rec{p, r.Last})
	}
	h.mu.Unlock()
	sort.Slice(recs, func(i, j int) bool { return recs[i].last > recs[j].last })
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	out := []Hit{}
	for _, r := range recs {
		if len(out) >= limit {
			break
		}
		if id, ok := ld.store.Resolve(r.path); ok {
			out = append(out, hitOf(ld.store, id, false))
		}
	}
	return out
}

// openTimes: when each opened entry of s was last opened (for the "opened"
// order).
func (ld *Laserdot) openTimes(s *index.Store) map[uint32]int64 {
	h := &ld.opens
	h.mu.Lock()
	defer h.mu.Unlock()
	ld.loadOpens()
	out := make(map[uint32]int64, len(h.records))
	for p, r := range h.records {
		if id, ok := s.Resolve(p); ok {
			out[id] = r.Last
		}
	}
	return out
}

// addOpened fills in when this Mac's results were last opened.
func (ld *Laserdot) addOpened(r *SearchResults) {
	h := &ld.opens
	h.mu.Lock()
	defer h.mu.Unlock()
	ld.loadOpens()
	if len(h.records) == 0 {
		return
	}
	for i := range r.Hits {
		if r.Hits[i].Volume == nil {
			r.Hits[i].Opened = h.records[r.Hits[i].Path].Last
		}
	}
}
