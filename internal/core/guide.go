package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/query"
)

// Guidance that needs the index (spec §8.3 coaching for empty results,
// §8.9.5 live counts in the cheat sheet).

// Coaching kinds.
const (
	CoachIndexFolder = "index_folder"
	CoachFixValue    = "fix_value"
	CoachRelax       = "relax"
	CoachShowSyntax  = "show_syntax"
)

// Coaching is one suggestion for a query that found nothing. The JSON
// form is the protocol's: {"kind":"relax","removed":…,"query":…,"count":…}.
type Coaching struct {
	Kind    string
	Path    string // index_folder
	From    string // fix_value
	To      string // fix_value
	Removed string // relax
	Query   string // fix_value, relax
	Count   uint64 // fix_value, relax
}

// MarshalJSON writes only the fields of the kind.
func (c Coaching) MarshalJSON() ([]byte, error) {
	m := map[string]any{"kind": c.Kind}
	switch c.Kind {
	case CoachIndexFolder:
		m["path"] = c.Path
	case CoachFixValue:
		m["from"], m["to"], m["query"], m["count"] = c.From, c.To, c.Query, c.Count
	case CoachRelax:
		m["removed"], m["query"], m["count"] = c.Removed, c.Query, c.Count
	}
	return json.Marshal(m)
}

// UnmarshalJSON reads the protocol form.
func (c *Coaching) UnmarshalJSON(b []byte) error {
	var m struct {
		Kind, Path, From, To, Removed, Query string
		Count                                uint64
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	*c = Coaching{m.Kind, m.Path, m.From, m.To, m.Removed, m.Query, m.Count}
	return nil
}

// MaxCoaching suggestions are shown.
const MaxCoaching = 3

// ValueCount is a live count for one enumerated filter value.
type ValueCount struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Count uint64 `json:"count"`
}

// cut removes the runes [start, end) of q and tidies spaces.
func cut(q string, start, end int) string {
	r := []rune(q)
	start, end = min(start, len(r)), min(end, len(r))
	return strings.Join(strings.Fields(string(r[:start])+" "+string(r[end:])), " ")
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~"); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, rest)
		}
	}
	return p
}

func (ld *Laserdot) count(q string) uint64 {
	r, err := ld.Search(q, SearchOptions{Page: index.Page{Limit: 1}, TypoFallback: true})
	if err != nil {
		return 0
	}
	return r.Total
}

func (ld *Laserdot) indexed(p string) bool {
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	if _, ok := ld.store.Resolve(p); ok {
		return true
	}
	for _, d := range ld.drives {
		if _, ok := d.store.Resolve(p); ok {
			return true
		}
	}
	return false
}

func (ld *Laserdot) textValues(id query.FilterID) []string {
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	v := ld.store.TextValues(id)
	for _, d := range ld.drives {
		v = append(v, d.store.TextValues(id)...)
	}
	slices.Sort(v)
	return slices.Compact(v)
}

// Coach suggests what to do about a query that found nothing: index the
// folder it names, fix a misspelled value, or drop the term that empties
// the results.
func (ld *Laserdot) Coach(q string) []Coaching {
	parsed, err := query.Parse(q)
	if err != nil {
		return []Coaching{{Kind: CoachShowSyntax}}
	}
	out := []Coaching{}
	type relax struct {
		count   uint64
		removed string
		query   string
	}
	var relaxes []relax
	runes := []rune(q)
	nclauses := len(parsed.Clauses)
	if parsed.Raw != nil {
		nclauses++
	}
	for _, c := range parsed.Clauses {
		if len(c.Terms) == 0 {
			continue
		}
		start, end := c.Terms[0].Span.Start, c.Terms[len(c.Terms)-1].Span.End
		for _, t := range c.Terms {
			if t.Kind != query.TermFilter {
				continue
			}
			f := t.Filter
			if f.Def.Value.Kind == query.VPath {
				p := expandHome(f.Value.Path)
				if c, err := canonical(p); err == nil {
					p = c
				}
				if fi, err := os.Stat(p); err == nil && fi.IsDir() && !ld.indexed(p) {
					out = append(out, Coaching{Kind: CoachIndexFolder, Path: p})
				}
			}
			if f.Def.Value.Kind != query.VText {
				continue
			}
			known := ld.textValues(f.Def.ID)
			for _, v := range f.Value.Text {
				if v.Phrase || v.Wildcards || slices.ContainsFunc(known, func(k string) bool { return strings.Contains(k, v.Text) }) {
					continue
				}
				best, bestD := "", -1
				limit := max(2, utf8.RuneCountInString(v.Text)/4)
				for _, k := range known {
					d := query.EditDistance(v.Text, k)
					if d <= limit && d < utf8.RuneCountInString(k) && (bestD < 0 || d < bestD || d == bestD && k < best) {
						best, bestD = k, d
					}
				}
				if bestD < 0 {
					continue
				}
				token := string(runes[t.Span.Start:min(t.Span.End, len(runes))])
				fixed := strings.Replace(token, v.Text, best, 1)
				nq := strings.TrimSpace(cut(q, t.Span.Start, t.Span.End) + " " + fixed)
				if n := ld.count(nq); n > 0 {
					out = append(out, Coaching{Kind: CoachFixValue, From: v.Text, To: best, Query: nq, Count: n})
				}
			}
		}
		if nclauses > 1 {
			nq := cut(q, start, end)
			if n := ld.count(nq); n > 0 {
				relaxes = append(relaxes, relax{n, string(runes[start:min(end, len(runes))]), nq})
			}
		}
	}
	slices.SortStableFunc(relaxes, func(a, b relax) int {
		if a.count != b.count {
			if a.count > b.count {
				return -1
			}
			return 1
		}
		// Dropping a filter before dropping a word.
		ac, bc := strings.Contains(a.removed, ":"), strings.Contains(b.removed, ":")
		switch {
		case ac && !bc:
			return -1
		case bc && !ac:
			return 1
		}
		return 0
	})
	for _, r := range relaxes[:min(2, len(relaxes))] {
		out = append(out, Coaching{Kind: CoachRelax, Removed: r.removed, Query: r.query, Count: r.count})
	}
	if len(out) > MaxCoaching {
		out = out[:MaxCoaching]
	}
	if len(out) < MaxCoaching {
		out = append(out, Coaching{Kind: CoachShowSyntax})
	}
	return out
}

// ValueCounts are live counts for every enumerated and boolean filter
// value.
func (ld *Laserdot) ValueCounts() []ValueCount {
	out := []ValueCount{}
	for i := range query.Filters {
		f := &query.Filters[i]
		var values []string
		switch f.Value.Kind {
		case query.VEnum:
			values = f.Value.Values
		case query.VBool:
			values = []string{"true", "false"}
		default:
			continue
		}
		key := f.Key
		if p, _, ok := strings.Cut(f.Example, "."); ok && len(p) == 1 {
			key = p + "." + f.Key
		}
		for _, v := range values {
			out = append(out, ValueCount{Key: f.Key, Value: v, Count: ld.count(key + ":" + v)})
		}
	}
	return out
}
