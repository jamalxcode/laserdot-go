package core

import (
	"slices"
	"strings"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/index"
)

// MonthCount: how many of a search's results last changed in one month.
type MonthCount struct {
	Month string `json:"month"` // "2024-03"
	Count uint64 `json:"count"`
}

// Timeline (the owner chose "a timeline strip"): a search's results counted by
// the month they last changed, across this Mac's index and every attached
// drive, oldest first; months with none are left out (the app draws the
// gaps). Without a query, empty.
func (ld *Laserdot) Timeline(q string) ([]MonthCount, error) {
	if strings.TrimSpace(q) == "" || strings.HasPrefix(strings.TrimSpace(q), ">") {
		return []MonthCount{}, nil
	}
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	stores := []*index.Store{ld.store}
	contents := []*index.ContentIndex{ld.content}
	for _, d := range ld.drives {
		stores = append(stores, d.store)
		contents = append(contents, nil)
	}
	counts := map[string]uint64{}
	for i, s := range stores {
		hits, err := queryHits(s, contents[i], q)
		if err != nil {
			return nil, err
		}
		it := hits.Iterator()
		for it.HasNext() {
			m := s.Modified(it.Next())
			if m <= 0 {
				continue
			}
			counts[time.Unix(m, 0).Format("2006-01")]++
		}
	}
	out := make([]MonthCount, 0, len(counts))
	for m, n := range counts {
		out = append(out, MonthCount{m, n})
	}
	slices.SortFunc(out, func(a, b MonthCount) int { return strings.Compare(a.Month, b.Month) })
	return out, nil
}
