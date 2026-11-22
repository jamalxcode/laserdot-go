package core

// Searching this Mac's index and the drives': matching, ranking, hits.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/crash"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/media"
	"github.com/jamalxcode/laserdot-go/internal/query"
)

// Search runs query against the index (names, then contents).
func (ld *Laserdot) Search(q string, opts SearchOptions) (*SearchResults, error) {
	r, err := ld.search(q, opts)
	if err == nil && r != nil {
		ld.dropMissing(r)
		ld.addOpened(r)
		ld.noteDemand(q, r)
	}
	return r, err
}

// dropMissing leaves out results whose file is gone (deleted while the
// change was missed, say) and has their folders listed again in the
// background, so the index forgets them too. Only this Mac's results are
// checked (a drive could be a slow network volume); a file macOS will not
// let Laserdot see is kept.
func (ld *Laserdot) dropMissing(r *SearchResults) {
	var missing []int
	for i, h := range r.Hits {
		if h.Volume == nil {
			if _, err := os.Lstat(h.Path); errors.Is(err, fs.ErrNotExist) {
				missing = append(missing, i)
			}
		}
	}
	if len(missing) == 0 {
		return
	}
	// An indexed folder that is itself missing is offline (its entries stay
	// searchable for 30 days, §10.3): only files whose indexed folder is
	// still there count as deleted.
	roots, _ := ld.db.Roots()
	rootOf := func(p string) string {
		best := ""
		for _, rt := range roots {
			if within(p, rt.Path) && len(rt.Path) > len(best) {
				best = rt.Path
			}
		}
		return best
	}
	drop := map[int]bool{}
	var gone []string
	for _, i := range missing {
		p := r.Hits[i].Path
		if root := rootOf(p); root != "" {
			if _, err := os.Stat(root); err == nil {
				drop[i] = true
				gone = append(gone, p)
			}
		}
	}
	if len(gone) == 0 {
		return
	}
	kept := r.Hits[:0]
	for i, h := range r.Hits {
		if !drop[i] {
			kept = append(kept, h)
		}
	}
	r.Hits = kept
	r.Total -= min(r.Total, uint64(len(gone)))
	var changes []Change
	for _, p := range gone {
		dir := filepath.Dir(p)
		if _, busy := ld.fixing.LoadOrStore(dir, true); !busy {
			changes = append(changes, Change{Path: dir})
		}
	}
	if len(changes) == 0 {
		return
	}
	ld.background.Add(1)
	go func() {
		defer ld.background.Done()
		defer crash.Guard("removing deleted files")
		_, _ = ld.ApplyChanges(changes)
		for _, c := range changes {
			ld.fixing.Delete(c.Path)
		}
	}()
}

func (ld *Laserdot) search(q string, opts SearchOptions) (*SearchResults, error) {
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	if opts.Page.Sort == index.Relevance {
		opts.Page.Boost = ld.openBoosts(ld.store)
	}
	if opts.Page.Sort == index.ByOpened {
		opts.Page.Opened = ld.openTimes(ld.store)
	}
	if len(ld.drives) == 0 {
		r, _, err := searchStoreKeyed(ld.store, ld.content, ld.archives, q, opts)
		return r, err
	}
	// This Mac's index and every attached drive's, merged into one ranking.
	t := time.Now()
	want := opts.Page.Offset + opts.Page.Limit
	sub := opts
	sub.Page.Offset, sub.Page.Limit = 0, want
	type keyed struct {
		key index.RankKey
		src int
		hit Hit
	}
	var merged []keyed
	out := &SearchResults{Hits: []Hit{}, Warnings: []query.Warning{}}
	var firstErr error
	ok := false
	for n := 0; n <= len(ld.drives); n++ {
		s, content, archives := ld.store, ld.content, ld.archives
		var volume *string
		if n > 0 {
			d := ld.drives[n-1]
			s, content, archives, volume = d.store, nil, nil, &d.name // content and zip search cover this Mac only
		}
		sub.Page.Boost, sub.Page.Opened = nil, nil // the open history is this Mac's (ids of its store)
		if n == 0 {
			sub.Page.Boost, sub.Page.Opened = opts.Page.Boost, opts.Page.Opened
		}
		r, keys, err := searchStoreKeyed(s, content, archives, q, sub)
		if err != nil {
			// e.g. an in: folder only one index has
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ok = true
		out.Total += r.Total
		out.TypoFallback = out.TypoFallback || r.TypoFallback
		if len(out.Warnings) == 0 {
			out.Warnings = r.Warnings
		}
		for i, h := range r.Hits {
			h.Volume = volume
			merged = append(merged, keyed{keys[i], n, h})
		}
	}
	if !ok {
		return nil, firstErr
	}
	slices.SortStableFunc(merged, func(a, b keyed) int {
		switch {
		case a.key.Less(b.key):
			return -1
		case b.key.Less(a.key):
			return 1
		}
		return a.src - b.src
	})
	for i := opts.Page.Offset; i < min(len(merged), want); i++ {
		out.Hits = append(out.Hits, merged[i].hit)
	}
	out.ElapsedUS = uint64(time.Since(t).Microseconds())
	return out, nil
}

// SearchStore runs a query against any store (also used by benchmarks).
func SearchStore(s *index.Store, q string, opts SearchOptions) (*SearchResults, error) {
	r, _, err := searchStoreKeyed(s, nil, nil, q, opts)
	return r, err
}

// contentClass orders content matches: documents, then other files, then
// code, then anything hidden.
func contentClass(s *index.Store, id uint32) int {
	switch {
	case s.IsHidden(id):
		return 3
	case s.Kind(id) == media.Document:
		return 0
	case s.Kind(id) == media.Code:
		return 2
	}
	return 1
}

// contentRankBase: content hits rank after every name hit (§2.4.2), in
// BM25 order.
const contentRankBase = 1 << 60 // above every exact name key (< 2^56), below typo keys (≥ 2^62)

// searchStoreKeyed is a search plus each hit's sort key, for merging
// across indexes. content may be nil (a drive's index has none).
func searchStoreKeyed(s *index.Store, content *index.ContentIndex, archives *index.ArchiveIndex, q string, opts SearchOptions) (*SearchResults, []index.RankKey, error) {
	t := time.Now()
	parsed, err := query.Parse(q)
	if err != nil {
		return nil, nil, err
	}
	parts, err := splitContent(parsed)
	if err != nil {
		return nil, nil, err
	}
	pq := parts.query
	res := &SearchResults{Hits: []Hit{}, Warnings: pq.Warnings}
	if res.Warnings == nil {
		res.Warnings = []query.Warning{}
	}
	if pq.IsEmpty() && len(parts.filters) == 0 {
		res.ElapsedUS = uint64(time.Since(t).Microseconds())
		return res, nil, nil
	}
	// content: terms: a set every result must (or must not) be in.
	var must *roaring.Bitmap
	mustNot := roaring.New()
	for _, f := range parts.filters {
		set := roaring.New()
		if content != nil {
			paths, err := content.Matching(f.expr)
			if err != nil {
				return nil, nil, err
			}
			set.AddMany(contentIDs(s, paths))
		}
		if f.negated {
			mustNot.Or(set)
		} else if must == nil {
			must = set
		} else {
			must.And(set)
		}
	}
	restrict := func(b *roaring.Bitmap) *roaring.Bitmap {
		if must != nil {
			b.And(must)
		}
		b.AndNot(mustNot)
		return b
	}
	exact, err := query.Compile(pq, false)
	if err != nil {
		return nil, nil, err
	}
	exactHits, err := s.Execute(exact)
	if err != nil {
		return nil, nil, err
	}
	exactHits = restrict(exactHits)
	ranked := s.Rank(exactHits, exact, opts.Page, 0)
	res.Total = exactHits.GetCardinality()
	typoIDs, contentHit := roaring.New(), roaring.New()
	nameHits := exactHits.Clone()
	p := opts.Page
	if opts.TypoFallback && res.Total < TypoFallbackBelow && query.HasTypoCandidates(pq) {
		typo, err := query.Compile(pq, true)
		if err != nil {
			return nil, nil, err
		}
		hits, err := s.Execute(typo)
		if err != nil {
			return nil, nil, err
		}
		extra := roaring.AndNot(restrict(hits), exactHits)
		if !extra.IsEmpty() {
			res.TypoFallback = true
			res.Total += extra.GetCardinality()
			nameHits.Or(extra)
			// Exact matches fill the page first; typo matches follow.
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
	// Plain words also match contents (§2.4.2): entries that satisfy the
	// rest of the query, ranked after every name match.
	if expr, ok := index.FTSQuery(parts.words, true); content != nil && ok {
		rest, err := query.Compile(parts.withoutWords, false)
		if err != nil {
			return nil, nil, err
		}
		allowed, err := s.Execute(rest)
		if err != nil {
			return nil, nil, err
		}
		allowed = roaring.AndNot(restrict(allowed), nameHits)
		paths, err := content.Search(expr, ContentResults)
		if err != nil {
			return nil, nil, err
		}
		var cids []uint32
		for _, id := range contentIDs(s, paths) {
			if allowed.Contains(id) {
				cids = append(cids, id)
			}
		}
		// Documents before other files, code last (each in word-match
		// order): what people search contents for is their writing.
		slices.SortStableFunc(cids, func(a, b uint32) int { return contentClass(s, a) - contentClass(s, b) })
		if len(cids) > 0 {
			shown := int(nameHits.GetCardinality())
			offset := max(p.Offset-shown, 0)
			room := min(max(p.Offset+p.Limit-shown, 0), p.Limit)
			res.Total += uint64(len(cids))
			var add []index.Ranked
			if p.Sort == index.Relevance {
				for i := offset; i < min(len(cids), offset+room); i++ {
					add = append(add, index.Ranked{ID: cids[i], Key: index.RankKey{Num: contentRankBase + uint64(i)}})
				}
			} else {
				set := roaring.New()
				set.AddMany(cids)
				add = s.Rank(set, rest, index.Page{Sort: p.Sort, Reverse: p.Reverse, Offset: offset, Limit: room}, 0)
			}
			for _, r := range add {
				contentHit.Add(r.ID)
			}
			ranked = append(ranked, add...)
			if len(ranked) > p.Limit {
				ranked = ranked[:p.Limit]
			}
		}
	}
	// Plain words also match the names inside zip files: the zip is a
	// result, after the name and content matches, with what it contains.
	inside := map[uint32][]string{}
	if archives != nil && len(parts.words) > 0 && wordsLongEnough(parts.words, ArchiveMinWord) {
		add, total, err := archiveMatches(s, archives, parts, restrict, nameHits, contentHit, p, int(res.Total), inside)
		if err != nil {
			return nil, nil, err
		}
		res.Total += total
		ranked = append(ranked, add...)
		if len(ranked) > p.Limit {
			ranked = ranked[:p.Limit]
		}
	}
	keys := make([]index.RankKey, 0, len(ranked))
	for _, r := range ranked {
		h := hitOf(s, r.ID, typoIDs.Contains(r.ID))
		h.Content = contentHit.Contains(r.ID)
		h.Inside = inside[r.ID]
		res.Hits = append(res.Hits, h)
		keys = append(keys, r.Key)
	}
	res.ElapsedUS = uint64(time.Since(t).Microseconds())
	return res, keys, nil
}

func hitOf(s *index.Store, id uint32, typo bool) Hit {
	h := Hit{
		Path: s.Path(id), Name: s.Name(id), Kind: s.Kind(id).String(), IsDir: s.IsDir(id),
		Size: s.Size(id), Modified: s.Modified(id), Created: s.Created(id), Typo: typo,
		Media: s.MediaSummary(id),
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
