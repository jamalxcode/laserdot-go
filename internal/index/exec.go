package index

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/jamalxcode/laserdot-go/internal/media"
	"github.com/jamalxcode/laserdot-go/internal/query"
)

// Sort is a result order.
type Sort int

const (
	// Relevance: best match first; for filter-only queries, most recently
	// modified.
	Relevance Sort = iota
	ByName
	BySize
	ByModified
	ByCreated
)

// ParseSort reads a sort name ("relevance", "name", "size", "modified",
// "created").
func ParseSort(s string) (Sort, bool) {
	switch s {
	case "", "relevance":
		return Relevance, true
	case "name":
		return ByName, true
	case "size":
		return BySize, true
	case "modified":
		return ByModified, true
	case "created":
		return ByCreated, true
	}
	return 0, false
}

func (s Sort) String() string {
	return [...]string{"relevance", "name", "size", "modified", "created"}[s]
}

// Page selects an order and a window of results.
type Page struct {
	Sort Sort
	// Reverse the sort's natural direction (size and dates: largest or
	// newest first; name: A→Z).
	Reverse bool
	Offset  int
	Limit   int
}

// DefaultPage is relevance order, first 100.
var DefaultPage = Page{Limit: 100}

// Execute runs q, returning every matching id. Clauses are ANDed in cost
// order (bitmap filters, then column ranges, then name regexes), so regex
// verification only touches what cheap filters left (§2.1.1e).
func (s *Store) Execute(q *query.Compiled) (*roaring.Bitmap, error) {
	cand := s.Live()
	for _, r := range s.roots {
		cand.Remove(r)
	}
	type step struct {
		class    int
		estimate uint64
		clause   *query.CClause
		pre      []*roaring.Bitmap
	}
	plan := make([]step, 0, len(q.Clauses))
	for i := range q.Clauses {
		c := &q.Clauses[i]
		st := step{clause: c, pre: make([]*roaring.Bitmap, len(c.Terms))}
		for j := range c.Terms {
			t := &c.Terms[j]
			if t.Kind == query.CName {
				st.pre[j] = s.prefilter(t.Name)
			}
			st.class = max(st.class, cost(t))
		}
		if st.class >= 2 {
			for _, p := range st.pre {
				if p == nil {
					st.estimate += s.all.GetCardinality()
				} else {
					st.estimate += p.GetCardinality()
				}
			}
		}
		plan = append(plan, st)
	}
	sort.SliceStable(plan, func(i, j int) bool {
		if plan[i].class != plan[j].class {
			return plan[i].class < plan[j].class
		}
		return plan[i].estimate < plan[j].estimate
	})
	for _, st := range plan {
		if cand.IsEmpty() {
			break
		}
		matched := roaring.New()
		for j := range st.clause.Terms {
			t := &st.clause.Terms[j]
			var m *roaring.Bitmap
			switch t.Kind {
			case query.CName:
				pool := cand
				if p := st.pre[j]; p != nil {
					pool = roaring.And(p, cand)
				}
				m = s.matchNames(t.Name, pool)
			case query.CFilterTerm:
				var err error
				if m, err = s.filter(&t.Filter, cand); err != nil {
					return nil, err
				}
			case query.CGroup:
				// File-level versions of each filter narrow the pool, then
				// tracks are checked one by one.
				pool := cand
				for k := range t.Group {
					var err error
					if pool, err = s.filter(&t.Group[k], pool); err != nil {
						return nil, err
					}
				}
				m = s.group(t.Scope, t.Group, pool)
			}
			if t.Negated {
				m = roaring.AndNot(cand, m)
			}
			matched.Or(m)
		}
		cand.And(matched)
	}
	return cand, nil
}

func cost(t *query.CTerm) int {
	switch t.Kind {
	case query.CFilterTerm:
		if query.ByID(t.Filter.ID).Value.Kind.IsInterval() {
			return 1
		}
		return 0
	case query.CGroup:
		return 1
	}
	return 2
}

// prefilter: entries that can possibly match m, from the trigram index;
// nil when there is no usable literal (every candidate must be checked).
func (s *Store) prefilter(m *query.NameMatcher) *roaring.Bitmap {
	if m.OnPath {
		return nil
	}
	var pool *roaring.Bitmap
groups:
	for _, group := range m.Prefilter {
		anyOf := roaring.New()
		for _, lit := range group {
			tg := s.TrigramCandidates(lit)
			if tg == nil {
				continue groups // a literal too short to filter on
			}
			anyOf.Or(tg)
		}
		if pool == nil {
			pool = anyOf
		} else {
			pool.And(anyOf)
		}
	}
	return pool
}

// matchNames: entries of pool whose folded name (or path) matches m, checked in
// parallel.
func (s *Store) matchNames(m *query.NameMatcher, pool *roaring.Bitmap) *roaring.Bitmap {
	if m.OnPath {
		return s.scan(pool, 256, func(id uint32) bool { return m.Regex.MatchString(s.FoldedPath(id)) })
	}
	return s.scan(pool, 4096, func(id uint32) bool { return m.Regex.Match(s.folded.bytes(id)) })
}

// scan keeps the ids of pool for which keep is true, in parallel chunks of
// at least minChunk ids.
func (s *Store) scan(pool *roaring.Bitmap, minChunk int, keep func(uint32) bool) *roaring.Bitmap {
	ids := pool.ToArray()
	workers := runtime.GOMAXPROCS(0)
	if len(ids) < 2*minChunk || workers == 1 {
		out := roaring.New()
		for _, id := range ids {
			if keep(id) {
				out.Add(id)
			}
		}
		return out
	}
	chunks := min(workers*4, len(ids)/minChunk)
	parts := make([][]uint32, chunks)
	var wg sync.WaitGroup
	for c := 0; c < chunks; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			var hit []uint32
			for _, id := range ids[c*len(ids)/chunks : (c+1)*len(ids)/chunks] {
				if keep(id) {
					hit = append(hit, id)
				}
			}
			parts[c] = hit
		}(c)
	}
	wg.Wait()
	out := roaring.New()
	for _, p := range parts {
		out.AddMany(p)
	}
	return out
}

func (s *Store) filter(f *query.CFilter, cand *roaring.Bitmap) (*roaring.Bitmap, error) {
	iv := f.Value.Interval
	inRange := func(pool *roaring.Bitmap, iv query.Interval, col func(uint32) float64) *roaring.Bitmap {
		return s.scan(pool, 8192, func(id uint32) bool { return iv.Contains(col(id)) })
	}
	enumUnion := func(vals, table []string, bms []*roaring.Bitmap) *roaring.Bitmap {
		out := roaring.New()
		for _, v := range vals {
			if i := slices.Index(table, v); i >= 0 {
				out.Or(bms[i])
			}
		}
		out.And(cand)
		return out
	}
	textUnion := func(m map[string]*roaring.Bitmap) *roaring.Bitmap {
		out := roaring.New()
		for k, bm := range m {
			for _, nm := range f.Value.Text {
				if nm.Match(k) {
					out.Or(bm)
					break
				}
			}
		}
		out.And(cand)
		return out
	}
	boolSet := func(want bool, set *roaring.Bitmap) *roaring.Bitmap {
		if want {
			return roaring.And(cand, set)
		}
		return roaring.AndNot(cand, set)
	}
	modified := func(id uint32) float64 { return float64(s.modified[id]) }
	withDims := func() *roaring.Bitmap { return roaring.And(cand, s.dimsBM) }

	switch f.ID {
	case query.FKind:
		return enumUnion(f.Value.Enum, query.Kinds, s.kindBM), nil
	case query.FExt:
		return textUnion(s.extBM), nil
	case query.FFsize:
		return inRange(roaring.AndNot(cand, s.dirBM), iv, func(id uint32) float64 { return float64(s.size[id]) }), nil
	case query.FModified:
		return inRange(cand, iv, modified), nil
	case query.FCreated:
		return inRange(cand, iv, func(id uint32) float64 { return float64(s.created[id]) }), nil
	case query.FSince:
		return inRange(cand, query.Interval{Lo: iv.Lo, Hi: math.Inf(1)}, modified), nil
	case query.FBefore:
		return inRange(cand, query.Interval{Lo: math.Inf(-1), Hi: iv.Lo}, modified), nil
	case query.FIn:
		dir, ok := s.resolveDir(f.Value.Path)
		if !ok {
			return nil, &query.Error{
				Message:    fmt.Sprintf("`%s` is not an indexed folder", f.Value.Path),
				Span:       f.Span,
				Suggestion: "laserdot index " + f.Value.Path,
			}
		}
		return roaring.And(s.Within(dir), cand), nil
	case query.FVolume:
		name := s.volume
		if name == "" {
			name = "local"
		}
		for _, nm := range f.Value.Text {
			if nm.Match(name) {
				return cand.Clone(), nil
			}
		}
		return roaring.New(), nil
	case query.FTag:
		return textUnion(s.tagBM), nil
	case query.FLocal:
		return boolSet(!f.Value.Bool, s.datalBM), nil
	case query.FHidden:
		return boolSet(f.Value.Bool, s.hiddenBM), nil
	case query.FRaw:
		if f.Value.Bool {
			return roaring.And(cand, s.rawBM), nil
		}
		return roaring.AndNot(roaring.And(cand, s.kindBM[media.Image]), s.rawBM), nil
	case query.FRes:
		return enumUnion(f.Value.Enum, query.Res, s.resBM[:]), nil
	case query.FOrientation:
		return enumUnion(f.Value.Enum, []string{"portrait", "landscape", "square"}, s.orientBM[:]), nil
	case query.FWidth:
		return inRange(withDims(), iv, func(id uint32) float64 { return float64(s.width[id]) }), nil
	case query.FHeight:
		return inRange(withDims(), iv, func(id uint32) float64 { return float64(s.height[id]) }), nil
	case query.FMegapixels:
		return inRange(withDims(), iv, func(id uint32) float64 {
			return float64(s.width[id]) * float64(s.height[id]) / 1e6
		}), nil
	}
	if r, ok := s.evalMedia(f, cand); ok {
		return r, nil
	}
	return nil, &query.Error{
		Message: fmt.Sprintf("`%s:` is not executable yet", query.ByID(f.ID).Key),
		Span:    f.Span,
	}
}

// resolveDir finds an indexed folder from user input (~ expanded,
// symlinks resolved when the path exists).
func (s *Store) resolveDir(raw string) (uint32, bool) {
	expanded := raw
	if rest, ok := strings.CutPrefix(raw, "~"); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
		home, _ := os.UserHomeDir()
		expanded = home + rest
	}
	var tries []string
	if abs, err := filepath.Abs(expanded); err == nil {
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			tries = append(tries, real)
		}
		tries = append(tries, abs)
	}
	tries = append(tries, expanded)
	for _, p := range tries {
		if id, ok := s.Resolve(p); ok && s.IsDir(id) {
			return id, true
		}
	}
	return 0, false
}

// nameClauses are the non-negated name terms of each clause, for scoring.
func nameClauses(q *query.Compiled) [][]*query.NameMatcher {
	var out [][]*query.NameMatcher
	for _, c := range q.Clauses {
		var ms []*query.NameMatcher
		for _, t := range c.Terms {
			if !t.Negated && t.Kind == query.CName {
				ms = append(ms, t.Name)
			}
		}
		if len(ms) > 0 {
			out = append(out, ms)
		}
	}
	return out
}

// relevanceKey: match quality, then shorter names, then shallower paths
// (§10.1). Lower is better.
func (s *Store) relevanceKey(clauses [][]*query.NameMatcher, penalty uint32, id uint32) uint64 {
	name := s.FoldedName(id)
	q := uint64(penalty)
	for _, clause := range clauses {
		best := query.NoMatch
		for _, m := range clause {
			var mq query.MatchQuality
			if m.OnPath {
				mq = query.NoMatch
				if m.Match(s.FoldedPath(id)) {
					mq = query.Substring
				}
			} else {
				mq = m.Quality(name)
			}
			best = min(best, mq)
		}
		if best == query.NoMatch {
			best = query.Substring
		}
		q += uint64(best)
	}
	return q<<40 | uint64(min(len(name), 0xFFFF))<<20 | uint64(s.depth[id])
}

// RankKey is a comparable sort key, so results from several indexes (this
// Mac and each attached drive) merge into one order.
type RankKey struct {
	Num uint64
	// Text is set for name order; Desc = Z→A.
	Text   string
	IsText bool
	Desc   bool
}

// Less orders keys in result order.
func (a RankKey) Less(b RankKey) bool {
	switch {
	case a.IsText && b.IsText:
		if a.Desc {
			return a.Text > b.Text
		}
		return a.Text < b.Text
	case a.IsText != b.IsText:
		return !a.IsText
	}
	return a.Num < b.Num
}

// Ranked is a result id with its sort key.
type Ranked struct {
	ID  uint32
	Key RankKey
}

func effectiveSort(page Page, clauses [][]*query.NameMatcher) (Sort, bool) {
	sort := page.Sort
	if sort == Relevance && len(clauses) == 0 {
		sort = ByModified
	}
	natural := sort == BySize || sort == ByModified || sort == ByCreated
	return sort, natural != page.Reverse
}

func (s *Store) colKey(sort Sort, id uint32) uint64 {
	switch sort {
	case BySize:
		return s.size[id]
	case ByModified:
		return uint64(s.modified[id]) ^ 1<<63
	}
	return uint64(s.created[id]) ^ 1<<63
}

// Rank orders hits and returns one page with each id's key. penalty is
// added to every relevance score (typo-only matches rank last).
func (s *Store) Rank(hits *roaring.Bitmap, q *query.Compiled, page Page, penalty uint32) []Ranked {
	want := page.Offset + page.Limit
	if want <= 0 || hits.IsEmpty() {
		return nil
	}
	clauses := nameClauses(q)
	srt, desc := effectiveSort(page, clauses)
	key := func(id uint32) RankKey {
		switch srt {
		case Relevance:
			return RankKey{Num: s.relevanceKey(clauses, penalty, id)}
		case ByName:
			return RankKey{Text: s.FoldedName(id), IsText: true, Desc: page.Reverse}
		}
		v := s.colKey(srt, id)
		if desc {
			v = ^v
		}
		return RankKey{Num: v}
	}
	window := func(ids []uint32) []Ranked {
		if page.Offset >= len(ids) {
			return nil
		}
		ids = ids[page.Offset:min(len(ids), want)]
		out := make([]Ranked, len(ids))
		for i, id := range ids {
			out[i] = Ranked{ID: id, Key: key(id)}
		}
		return out
	}

	// Large result sets in a column order: walk the pre-sorted order.
	if srt != Relevance && srt != ByName && hits.GetCardinality() > 20_000 {
		order := s.bySize
		switch srt {
		case ByModified:
			order = s.byModified
		case ByCreated:
			order = s.byCreated
		}
		// Base ids come from the pre-sorted order; ids appended since the
		// build are few, so they are sorted here and merged in.
		colKey := func(id uint32) uint64 {
			v := s.colKey(srt, id)
			if desc {
				v = ^v
			}
			return v
		}
		var added []uint32
		if int(s.baseLen) < s.Len() {
			it := hits.Iterator()
			it.AdvanceIfNeeded(s.baseLen)
			for it.HasNext() {
				added = append(added, it.Next())
			}
			slices.SortFunc(added, func(a, b uint32) int {
				ka, kb := colKey(a), colKey(b)
				switch {
				case ka < kb:
					return -1
				case ka > kb:
					return 1
				}
				return int(a) - int(b)
			})
		}
		ids := make([]uint32, 0, want)
		i := 0
		for len(ids) < want {
			var base uint32 = None
			for ; i < len(order); i++ {
				id := order[i]
				if desc {
					id = order[len(order)-1-i]
				}
				if hits.Contains(id) {
					base = id
					break
				}
			}
			if len(added) > 0 && (base == None || colKey(added[0]) < colKey(base)) {
				ids = append(ids, added[0])
				added = added[1:]
				continue
			}
			if base == None {
				break
			}
			ids = append(ids, base)
			i++
		}
		return window(ids)
	}

	ids := hits.ToArray()
	if srt == ByName {
		slices.SortFunc(ids, func(a, b uint32) int {
			if c := strings.Compare(s.FoldedName(a), s.FoldedName(b)); c != 0 {
				if page.Reverse {
					return -c
				}
				return c
			}
			return int(a) - int(b)
		})
		return window(ids)
	}
	type kv struct {
		k  uint64
		id uint32
	}
	keyed := make([]kv, len(ids))
	parallel(len(ids), func(i int) { keyed[i] = kv{key(ids[i]).Num, ids[i]} })
	cmp := func(a, b kv) int {
		switch {
		case a.k < b.k:
			return -1
		case a.k > b.k:
			return 1
		}
		return int(a.id) - int(b.id)
	}
	if len(keyed) > want {
		keyed = topK(keyed, want, cmp)
	}
	slices.SortFunc(keyed, cmp)
	ids = ids[:len(keyed)]
	for i := range keyed {
		ids[i] = keyed[i].id
	}
	return window(ids)
}

// topK returns the k smallest items of v (unordered), with a bounded
// max-heap: O(n log k) instead of sorting everything for one page.
func topK[T any](v []T, k int, cmp func(a, b T) int) []T {
	h := slices.Clone(v[:k])
	less := func(i, j int) bool { return cmp(h[i], h[j]) > 0 } // max-heap
	down := func(i int) {
		for {
			l, big := 2*i+1, i
			if l < k && less(l, big) {
				big = l
			}
			if r := l + 1; r < k && less(r, big) {
				big = r
			}
			if big == i {
				return
			}
			h[i], h[big] = h[big], h[i]
			i = big
		}
	}
	for i := k/2 - 1; i >= 0; i-- {
		down(i)
	}
	for _, x := range v[k:] {
		if cmp(x, h[0]) < 0 {
			h[0] = x
			down(0)
		}
	}
	return h
}
