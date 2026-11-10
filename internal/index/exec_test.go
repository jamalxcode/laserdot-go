package index

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jamalxcode/laserdot-go/internal/query"
)

// tree builds a store from "path|size|modified" lines under root /r.
func tree(t *testing.T, lines ...string) *Store {
	t.Helper()
	raw := []RawEntry{{Parent: -1, Name: "/r", IsDir: true}}
	idx := map[string]int{"": 0}
	for _, l := range lines {
		var path string
		var size uint64
		var mod int64
		parts := strings.Split(l, "|")
		path = parts[0]
		if len(parts) > 1 {
			for _, c := range parts[1] {
				size = size*10 + uint64(c-'0')
			}
		}
		if len(parts) > 2 {
			for _, c := range parts[2] {
				mod = mod*10 + int64(c-'0')
			}
		}
		dir := strings.HasSuffix(path, "/")
		path = strings.TrimSuffix(path, "/")
		parent, name := "", path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			parent, name = path[:i], path[i+1:]
		}
		p, ok := idx[parent]
		if !ok {
			t.Fatalf("parent of %s not listed", path)
		}
		idx[path] = len(raw)
		raw = append(raw, RawEntry{Parent: p, Name: name, IsDir: dir, Size: size, Modified: mod})
	}
	return Build(raw)
}

func search(t *testing.T, s *Store, q string, page Page) []string {
	t.Helper()
	pq, err := query.ParseAt(q, 1_762_732_800)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	c, err := query.Compile(pq, false)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	hits, err := s.Execute(c)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	var out []string
	for _, r := range s.Rank(hits, c, page, 0) {
		out = append(out, strings.TrimPrefix(s.Path(r.ID), "/r/"))
	}
	return out
}

func fixture(t *testing.T) *Store {
	return tree(t,
		"docs/", "docs/invoice-march.pdf|1000|300", "docs/march invoice.pdf|2000|200",
		"docs/reinvoiced.pdf|3000|100", "docs/receipt.pdf|10|400",
		"Movies/", "Movies/film.mkv|5000000000|50", "Movies/clip.mp4|1000|60",
		".cache/", ".cache/invoice.tmp|1|1", "invoice|5|5",
	)
}

func TestRelevanceOrder(t *testing.T) {
	got := search(t, fixture(t), "invoice", DefaultPage)
	want := []string{"invoice", ".cache/invoice.tmp", "docs/invoice-march.pdf", "docs/march invoice.pdf", "docs/reinvoiced.pdf"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestClausesAndNegation(t *testing.T) {
	s := fixture(t)
	if got := search(t, s, "invoice march -reinvoiced", DefaultPage); len(got) != 2 {
		t.Errorf("%q", got)
	}
	if got := search(t, s, "invoice hidden:false", DefaultPage); len(got) != 4 {
		t.Errorf("%q", got)
	}
	if got := search(t, s, "mkv OR mp4", Page{Sort: ByName, Limit: 10}); !reflect.DeepEqual(got, []string{"Movies/clip.mp4", "Movies/film.mkv"}) {
		t.Errorf("%q", got)
	}
}

func TestFileFilters(t *testing.T) {
	s := fixture(t)
	for q, want := range map[string][]string{
		"kind:video":            {"Movies/clip.mp4", "Movies/film.mkv"},
		"ext:mkv":               {"Movies/film.mkv"},
		"fsize:>1gb":            {"Movies/film.mkv"},
		"in:/r/docs pdf -march": {"docs/receipt.pdf", "docs/reinvoiced.pdf"},
		"kind:folder":           {".cache", "docs", "Movies"},
	} {
		if got := search(t, s, q, Page{Sort: ByName, Limit: 50}); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %q, want %q", q, got, want)
		}
	}
}

func TestInUnknownFolderIsPositionedError(t *testing.T) {
	s := fixture(t)
	pq, _ := query.ParseAt("x in:/nowhere", 0)
	c, _ := query.Compile(pq, false)
	_, err := s.Execute(c)
	var e *query.Error
	if !errors.As(err, &e) || e.Suggestion != "laserdot index /nowhere" || e.Span.Start != 2 {
		t.Errorf("%v", err)
	}
}

func TestSortsAndPaging(t *testing.T) {
	s := fixture(t)
	// Filter-only queries default to newest first.
	if got := search(t, s, "ext:pdf", DefaultPage); got[0] != "docs/receipt.pdf" {
		t.Errorf("%q", got)
	}
	if got := search(t, s, "ext:pdf", Page{Sort: BySize, Limit: 2}); !reflect.DeepEqual(got, []string{"docs/reinvoiced.pdf", "docs/march invoice.pdf"}) {
		t.Errorf("%q", got)
	}
	if got := search(t, s, "ext:pdf", Page{Sort: BySize, Reverse: true, Offset: 1, Limit: 1}); !reflect.DeepEqual(got, []string{"docs/invoice-march.pdf"}) {
		t.Errorf("%q", got)
	}
}

func TestLargeSortUsesPresortedOrder(t *testing.T) {
	lines := []string{"d/"}
	for i := 0; i < 25_000; i++ {
		lines = append(lines, "d/f"+strings.Repeat("x", i%7)+itoa(i)+"|"+itoa(i))
	}
	s := tree(t, lines...)
	got := search(t, s, "in:/r/d", Page{Sort: BySize, Limit: 3})
	if !reflect.DeepEqual(got, []string{"d/fxx24999", "d/fx24998", "d/f24997"}) {
		t.Errorf("%q", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

func TestResolveAndWithin(t *testing.T) {
	s := fixture(t)
	id, ok := s.Resolve("/r/docs/receipt.pdf")
	if !ok || s.Name(id) != "receipt.pdf" {
		t.Fatal("resolve")
	}
	d, _ := s.Resolve("/r/docs")
	if s.Within(d).GetCardinality() != 4 {
		t.Error("within")
	}
}
