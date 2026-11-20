package core

import (
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/jamalxcode/laserdot-go/internal/crash"
	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/media"
	"github.com/jamalxcode/laserdot-go/internal/query"
)

// Zip contents (the owner chose "search inside zip files"): the names of
// the files inside this Mac's zips are listed (from each zip's table of
// contents; nothing is unpacked) and plain words match them like file
// names. The zip is the result, after the name and content matches, with
// the matching names inside it.

// archiveRankBase: zips found by their contents rank after content hits.
const archiveRankBase = contentRankBase + 1<<59

// ArchiveReaders: zips read at once.
const ArchiveReaders = 8

// InsideShown: matching names reported per zip.
const InsideShown = 3

func archivesPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "archives.db")
}

// ArchiveStats: what an archive update did.
type ArchiveStats struct {
	Listed     uint64 `json:"listed"`
	Unreadable uint64 `json:"unreadable"`
	Removed    uint64 `json:"removed"`
	MS         uint64 `json:"ms"`
}

// ArchiveJob is an update of the zip listings to run (outside any lock).
type ArchiveJob struct {
	ai     *index.ArchiveIndex
	todo   []index.ArchiveDoc // Path and Stamp set; Names to read
	remove []string
}

// IsEmpty: nothing to do.
func (j *ArchiveJob) IsEmpty() bool { return len(j.todo) == 0 && len(j.remove) == 0 }

// ArchiveJob works out which zips are new, changed or gone.
func (ld *Laserdot) ArchiveJob() *ArchiveJob {
	stamps := ld.archives.Stamps()
	ld.mu.RLock()
	wanted := map[string]index.DocStamp{}
	s := ld.store
	it := s.Live().Iterator()
	for it.HasNext() {
		id := it.Next()
		if s.IsDir(id) || s.IsSymlink(id) || s.IsDataless(id) || s.IsPending(id) || s.Kind(id) != media.Archive {
			continue
		}
		if !index.IsArchiveExt(media.Extension(s.FoldedName(id))) {
			continue
		}
		wanted[s.Path(id)] = index.DocStamp{Size: s.Size(id), Modified: s.Modified(id)}
	}
	ld.mu.RUnlock()
	j := &ArchiveJob{ai: ld.archives}
	for p, st := range wanted {
		if old, ok := stamps[p]; !ok || old != st {
			j.todo = append(j.todo, index.ArchiveDoc{Path: p, Stamp: st})
		}
	}
	slices.SortFunc(j.todo, func(a, b index.ArchiveDoc) int { return strings.Compare(a.Path, b.Path) })
	for p := range stamps {
		if _, ok := wanted[p]; !ok {
			j.remove = append(j.remove, p)
		}
	}
	return j
}

// Run reads the zips' tables of contents and stores them.
func (j *ArchiveJob) Run() (ArchiveStats, error) {
	t := time.Now()
	var st ArchiveStats
	if err := j.ai.Remove(j.remove); err != nil {
		return st, err
	}
	st.Removed = uint64(len(j.remove))
	for start := 0; start < len(j.todo); start += 64 {
		batch := j.todo[start:min(start+64, len(j.todo))]
		// A few at a time: gentle on the disk while the Mac is in use.
		var wg sync.WaitGroup
		sem := make(chan struct{}, ArchiveReaders)
		for i := range batch {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				defer crash.Guard("listing a zip")
				names, ok := index.ListArchive(batch[i].Path)
				if ok {
					batch[i].Names = names
				}
			}()
		}
		wg.Wait()
		for _, d := range batch {
			if d.Names == nil {
				st.Unreadable++
			} else {
				st.Listed++
			}
		}
		if err := j.ai.Put(batch); err != nil {
			return st, err
		}
	}
	st.MS = uint64(time.Since(t).Milliseconds())
	return st, nil
}

// UpdateArchives brings the zip listings up to date now.
func (ld *Laserdot) UpdateArchives() (ArchiveStats, error) {
	return ld.ArchiveJob().Run()
}

// archiveMatches: the zips (not already results) holding a file whose name
// matches every plain word, and satisfying the rest of the query; one page
// of them, ranked after the shown results, and how many there are.
func archiveMatches(s *index.Store, archives *index.ArchiveIndex, parts *contentParts,
	restrict func(*roaring.Bitmap) *roaring.Bitmap, nameHits, contentHit *roaring.Bitmap,
	p index.Page, shown int, inside map[uint32][]string) ([]index.Ranked, uint64, error) {
	wq, err := query.Parse(strings.Join(quoteWords(parts.words), " "))
	if err != nil {
		return nil, 0, nil // a word the parser reads otherwise: no zip matching
	}
	wc, err := query.Compile(wq, false)
	if err != nil {
		return nil, 0, nil
	}
	var ms []*query.NameMatcher
	for _, c := range wc.Clauses {
		for _, t := range c.Terms {
			if t.Kind == query.CName && !t.Negated {
				ms = append(ms, t.Name)
			}
		}
	}
	if len(ms) == 0 {
		return nil, 0, nil
	}
	found := archives.Matching(func(f string) bool {
		for _, m := range ms {
			if !m.Match(f) {
				return false
			}
		}
		return true
	}, InsideShown)
	if len(found) == 0 {
		return nil, 0, nil
	}
	rest, err := query.Compile(parts.withoutWords, false)
	if err != nil {
		return nil, 0, err
	}
	allowed, err := s.Execute(rest)
	if err != nil {
		return nil, 0, err
	}
	allowed = restrict(allowed)
	allowed.AndNot(nameHits)
	allowed.AndNot(contentHit)
	var ids []uint32
	for path, names := range found {
		if id, ok := s.Resolve(path); ok && allowed.Contains(id) {
			ids = append(ids, id)
			inside[id] = names
		}
	}
	if len(ids) == 0 {
		return nil, 0, nil
	}
	slices.SortFunc(ids, func(a, b uint32) int { return strings.Compare(s.Path(a), s.Path(b)) })
	offset := max(p.Offset-shown, 0)
	room := min(max(p.Offset+p.Limit-shown, 0), p.Limit)
	var add []index.Ranked
	if p.Sort == index.Relevance {
		for i := offset; i < min(len(ids), offset+room); i++ {
			add = append(add, index.Ranked{ID: ids[i], Key: index.RankKey{Num: archiveRankBase + uint64(i)}})
		}
	} else {
		set := roaring.New()
		set.AddMany(ids)
		add = s.Rank(set, rest, index.Page{Sort: p.Sort, Reverse: p.Reverse, Offset: offset, Limit: room}, 0)
	}
	return add, uint64(len(ids)), nil
}

// quoteWords: plain words as name terms (a word like "march 2024" was
// already a date; here each is matched as a name).
func quoteWords(words []string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = `"` + strings.ReplaceAll(w, `"`, "") + `"`
	}
	return out
}
