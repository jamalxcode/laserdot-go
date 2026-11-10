package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/media"
	"github.com/jamalxcode/laserdot-go/internal/query"
)

// ContentResults: content matches considered per search.
const ContentResults = 1000

// ContentSettings say which folders get content indexing (§2.4.1).
type ContentSettings struct {
	Roots    []string `json:"roots"`
	MaxBytes uint64   `json:"max_bytes"`
}

// DefaultContentSettings: Documents, Desktop and Downloads.
func DefaultContentSettings() ContentSettings {
	home, _ := os.UserHomeDir()
	var roots []string
	if home != "" {
		for _, d := range []string{"Documents", "Desktop", "Downloads"} {
			roots = append(roots, filepath.Join(home, d))
		}
	}
	return ContentSettings{Roots: roots, MaxBytes: index.DefaultContentMaxBytes}
}

// ContentStats reports a content update.
type ContentStats struct {
	Indexed   uint64 `json:"indexed"`
	Skipped   uint64 `json:"skipped"`
	Removed   uint64 `json:"removed"`
	BytesRead uint64 `json:"bytes_read"`
	MS        uint64 `json:"ms"`
}

// ContentStatus is the state of content search.
type ContentStatus struct {
	Settings  *ContentSettings `json:"settings"`
	Documents uint64           `json:"documents"`
	Skipped   uint64           `json:"skipped"`
	Pending   uint64           `json:"pending"`
}

type contentTodo struct {
	path  string
	stamp index.DocStamp
	kind  index.ContentType
}

// ContentJob is a content update to run (outside any lock).
type ContentJob struct {
	ci       *index.ContentIndex
	todo     []contentTodo
	remove   []string
	maxBytes uint64
}

// Pending documents to read.
func (j *ContentJob) Pending() int { return len(j.todo) }

// IsEmpty: nothing to do.
func (j *ContentJob) IsEmpty() bool { return len(j.todo) == 0 && len(j.remove) == 0 }

// Run reads the documents and stores their text.
func (j *ContentJob) Run() (ContentStats, error) {
	t := time.Now()
	var st ContentStats
	if err := j.ci.Remove(j.remove); err != nil {
		return st, err
	}
	st.Removed = uint64(len(j.remove))
	for start := 0; start < len(j.todo); start += 64 {
		batch := j.todo[start:min(start+64, len(j.todo))]
		docs := make([]index.Doc, len(batch))
		var wg sync.WaitGroup
		for i, td := range batch {
			wg.Add(1)
			go func() {
				defer wg.Done()
				d := index.Doc{Path: td.path, Stamp: td.stamp}
				if td.stamp.Size <= j.maxBytes {
					d.Text, d.Indexed = index.ExtractText(td.path, td.kind)
				}
				docs[i] = d
			}()
		}
		wg.Wait()
		for _, d := range docs {
			if d.Indexed {
				st.Indexed++
				st.BytesRead += d.Stamp.Size
			} else {
				st.Skipped++
			}
		}
		if err := j.ci.Put(docs); err != nil {
			return st, err
		}
	}
	st.MS = uint64(time.Since(t).Milliseconds())
	return st, nil
}

func contentPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "content.db")
}

func (ld *Laserdot) loadContentSettings() {
	ld.contentSettings = DefaultContentSettings()
	if v, err := ld.db.Setting("content"); err == nil && v != "" {
		var s ContentSettings
		if json.Unmarshal([]byte(v), &s) == nil {
			if s.Roots == nil {
				s.Roots = []string{}
			}
			ld.contentSettings = s
		}
	}
}

// ContentSettings in effect.
func (ld *Laserdot) ContentSettings() ContentSettings {
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	return ld.contentSettings
}

// SetContentSettings changes which folders get content indexing.
func (ld *Laserdot) SetContentSettings(s ContentSettings) error {
	for i, r := range s.Roots {
		if c, err := canonical(r); err == nil {
			s.Roots[i] = c
		}
	}
	slices.Sort(s.Roots)
	s.Roots = slices.Compact(s.Roots)
	if s.Roots == nil {
		s.Roots = []string{}
	}
	if s.MaxBytes == 0 {
		s.MaxBytes = index.DefaultContentMaxBytes
	}
	b, _ := json.Marshal(s)
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	if err := ld.db.SetSetting("content", string(b)); err != nil {
		return err
	}
	ld.mu.Lock()
	ld.contentSettings = s
	ld.mu.Unlock()
	return nil
}

// ContentStatus reports content indexing.
func (ld *Laserdot) ContentStatus() (ContentStatus, error) {
	docs, skipped, err := ld.content.Counts()
	if err != nil {
		return ContentStatus{}, err
	}
	job, err := ld.ContentJob()
	if err != nil {
		return ContentStatus{}, err
	}
	s := ld.ContentSettings()
	return ContentStatus{Settings: &s, Documents: docs, Skipped: skipped, Pending: uint64(job.Pending())}, nil
}

// ContentJob works out what the content index lacks.
func (ld *Laserdot) ContentJob() (*ContentJob, error) {
	stamps, err := ld.content.Stamps()
	if err != nil {
		return nil, err
	}
	ld.mu.RLock()
	wanted := inScope(ld.store, ld.contentSettings.Roots)
	maxBytes := ld.contentSettings.MaxBytes
	ld.mu.RUnlock()
	j := &ContentJob{ci: ld.content, maxBytes: maxBytes}
	for p, w := range wanted {
		if st, ok := stamps[p]; !ok || st != w.stamp {
			j.todo = append(j.todo, w)
		}
	}
	slices.SortFunc(j.todo, func(a, b contentTodo) int { return strings.Compare(a.path, b.path) })
	for p := range stamps {
		if _, ok := wanted[p]; !ok {
			j.remove = append(j.remove, p)
		}
	}
	return j, nil
}

// UpdateContent brings the content index up to date now.
func (ld *Laserdot) UpdateContent() (ContentStats, error) {
	j, err := ld.ContentJob()
	if err != nil {
		return ContentStats{}, err
	}
	return j.Run()
}

// inScope: the documents under the content roots (and indexed roots inside
// them) that content search should hold.
func inScope(s *index.Store, roots []string) map[string]contentTodo {
	ids := roaring.New()
	indexed := s.Roots()
	for _, r := range roots {
		if dir, ok := s.Resolve(r); ok {
			ids.Or(s.Within(dir))
		}
		for _, ir := range indexed {
			if ir != r && within(ir, r) {
				if dir, ok := s.Resolve(ir); ok {
					ids.Or(s.Within(dir))
				}
			}
		}
	}
	out := map[string]contentTodo{}
	it := ids.Iterator()
	for it.HasNext() {
		id := it.Next()
		if s.IsDir(id) || s.IsSymlink(id) || s.IsDataless(id) || s.IsPending(id) {
			continue
		}
		kind := index.ContentTypeOf(media.Extension(s.FoldedName(id)))
		if kind == index.ContentNone {
			continue
		}
		p := s.Path(id)
		out[p] = contentTodo{p, index.DocStamp{Size: s.Size(id), Modified: s.Modified(id)}, kind}
	}
	return out
}

type contentFilter struct {
	negated bool
	expr    string
}

type contentParts struct {
	query        *query.Query
	filters      []contentFilter
	words        []string
	withoutWords *query.Query
}

// splitContent separates content: terms and the plain words that may also
// match contents from the rest of the query.
func splitContent(q *query.Query) (*contentParts, error) {
	parts := &contentParts{}
	kept := []query.Clause{}
	for _, c := range q.Clauses {
		ti := slices.IndexFunc(c.Terms, func(t query.Term) bool {
			return t.Kind == query.TermFilter && t.Filter.Def.ID == query.FContent
		})
		if ti < 0 {
			kept = append(kept, c)
			continue
		}
		term := c.Terms[ti]
		if len(c.Terms) > 1 {
			return nil, &query.Error{Message: "`content:` cannot be combined with OR", Span: term.Span}
		}
		f := term.Filter
		if len(f.Value.Text) == 0 {
			return nil, &query.Error{Message: "`content:` needs words", Span: f.Span}
		}
		var alts []string
		for _, v := range f.Value.Text {
			var expr string
			var ok bool
			switch {
			case v.Phrase || !v.Wildcards:
				expr, ok = index.FTSQuery([]string{v.Text}, false)
			default:
				stem, has := strings.CutSuffix(v.Text, "*")
				if !has || strings.ContainsAny(stem, "*?") {
					return nil, &query.Error{Message: "`content:` matches whole words: only a trailing `*` (prefix) is allowed", Span: f.Span}
				}
				expr, ok = index.FTSQuery([]string{stem}, true)
			}
			if !ok {
				return nil, &query.Error{Message: "`content:` needs letters or digits", Span: f.Span}
			}
			alts = append(alts, "("+expr+")")
		}
		parts.filters = append(parts.filters, contentFilter{term.Negated, strings.Join(alts, " OR ")})
	}
	nq := *q
	nq.Clauses = kept
	parts.query = &nq
	without := nq
	without.Clauses = nil
	for _, c := range kept {
		if len(c.Terms) == 1 && !c.Terms[0].Negated && c.Terms[0].Kind == query.TermText &&
			!c.Terms[0].Text.Wildcards && !strings.Contains(c.Terms[0].Text.Text, "/") {
			parts.words = append(parts.words, c.Terms[0].Text.Text)
			continue
		}
		without.Clauses = append(without.Clauses, c)
	}
	short := slices.ContainsFunc(parts.words, func(w string) bool { return utf8.RuneCountInString(w) < 2 })
	if nq.Raw != nil || short {
		parts.words = nil
		without = nq
	}
	parts.withoutWords = &without
	return parts, nil
}

// contentIDs maps document paths to live store ids.
func contentIDs(s *index.Store, paths []string) []uint32 {
	var out []uint32
	for _, p := range paths {
		if id, ok := s.Resolve(p); ok {
			out = append(out, id)
		}
	}
	return out
}
