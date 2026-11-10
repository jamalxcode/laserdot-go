package core

// Seam 1: external behaviour of the public API — real files in, results
// out. Story numbers refer to Issue 001. Media stories (13, 22) join in G2
// and rename tracking in G3.

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/index"
)

type fixture struct {
	root, db string
	ld       *Laserdot
}

func write(t *testing.T, root, rel string, data string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setMtime(t *testing.T, root, rel string, unix int64) {
	t.Helper()
	tm := time.Unix(unix, 0)
	if err := os.Chtimes(filepath.Join(root, rel), tm, tm); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "home")
	for rel, data := range map[string]string{
		"Documents/invoice-march-2024.pdf":    "pdf",
		"Documents/March Invoice.pdf":         "pdf",
		"Documents/invoice-april.pdf":         "pdf",
		"Documents/report(2).pdf":             "pdf",
		"Documents/report-final.docx":         "doc",
		"Documents/old-report.txt":            "txt",
		"Documents/file1.txt":                 "1",
		"Documents/file12.txt":                "12",
		"Documents/tax return draft.pdf":      "pdf",
		"Documents/tax return final.pdf":      "pdf",
		"Documents/فاتورة ٢٠٢٤.pdf":           "pdf",
		"Documents/أحمد.txt":                  "txt",
		"Documents/Café menu.txt":             "txt",
		"Movies/Big Trip 2024.mkv":            strings.Repeat("\x00", 5000),
		"Movies/sample.mkv":                   strings.Repeat("\x00", 50),
		"Movies/Holiday.mp4":                  strings.Repeat("\x00", 3000),
		"Pictures/IMG_0001.png":               "png",
		"Pictures/IMG_0002.png":               "png",
		"Pictures/RAW/DSC_0001.NEF":           "raw",
		"Code/node_modules/left-pad/index.js": "x",
		"Code/project/.git/objects/ab/cdef":   "x",
		"Code/project/main.rs":                "fn main(){}",
		".config/settings.toml":               "x",
	} {
		write(t, root, rel, data)
	}
	setMtime(t, root, "Documents/invoice-march-2024.pdf", 1_710_000_000) // 2024-03-09
	setMtime(t, root, "Documents/invoice-april.pdf", 1_580_000_000)      // 2020-01-26
	db := filepath.Join(root, ".local", "index.db")                      // inside the root on purpose
	ld, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ld.Close() })
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	root, _ = filepath.EvalSymlinks(root)
	return &fixture{root: root, db: db, ld: ld}
}

func (f *fixture) search(t *testing.T, q string) *SearchResults {
	t.Helper()
	r, err := f.ld.Search(q, DefaultSearchOptions)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return r
}

func (f *fixture) names(t *testing.T, q string) []string {
	t.Helper()
	out := []string{}
	for _, h := range f.search(t, q).Hits {
		out = append(out, h.Name)
	}
	return out
}

func sorted(v []string) []string {
	v = slices.Clone(v)
	slices.Sort(v)
	return v
}

func eq(t *testing.T, q string, got []string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %q, want %q", q, got, want)
	}
}

func TestStory1PlainWordsAnyOrder(t *testing.T) {
	f := newFixture(t)
	got := sorted(f.names(t, "invoice march"))
	eq(t, "invoice march", got, "March Invoice.pdf", "invoice-march-2024.pdf")
	eq(t, "march invoice", sorted(f.names(t, "march invoice")), got...)
}

func TestStory2Wildcards(t *testing.T) {
	f := newFixture(t)
	eq(t, "report*", sorted(f.names(t, "report*")), "report(2).pdf", "report-final.docx")
	eq(t, "file?.txt", f.names(t, "file?.txt"), "file1.txt")
	if n := len(f.names(t, "IMG_000?.png")); n != 2 {
		t.Errorf("IMG_000?.png: %d", n)
	}
}

func TestStory3RawRegex(t *testing.T) {
	f := newFixture(t)
	if n := len(f.names(t, `re:^IMG_\d{4}\.png$`)); n != 2 {
		t.Errorf("%d", n)
	}
	_, err := f.ld.Search("re:IMG_(", DefaultSearchOptions)
	if err == nil || !strings.Contains(err.Error(), "invalid regex") {
		t.Errorf("%v", err)
	}
}

func TestStory4TyposAndTranspositions(t *testing.T) {
	f := newFixture(t)
	for _, q := range []string{"inovice april", "invocie april", "invoce april", "invoicee april"} {
		r := f.search(t, q)
		if len(r.Hits) == 0 || r.Hits[0].Name != "invoice-april.pdf" || !r.TypoFallback || !r.Hits[0].Typo {
			t.Errorf("%s: %+v", q, r)
		}
	}
	if f.search(t, "invoice").TypoFallback {
		t.Error("plenty of exact hits → no typo fallback")
	}
}

func TestStory5SpecialCharactersAreLiteral(t *testing.T) {
	f := newFixture(t)
	eq(t, "report(2).pdf", f.names(t, "report(2).pdf"), "report(2).pdf")
}

func TestStory6FiltersWithText(t *testing.T) {
	f := newFixture(t)
	eq(t, "kind:video", sorted(f.names(t, "kind:video")), "Big Trip 2024.mkv", "Holiday.mp4", "sample.mkv")
	eq(t, "mkv >1kb", f.names(t, "kind:video ext:mkv fsize:>1kb"), "Big Trip 2024.mkv")
	eq(t, "2kb..4kb", f.names(t, "ext:mkv,mp4 fsize:2kb..4kb"), "Holiday.mp4")
	if n := len(f.names(t, "in:"+filepath.Join(f.root, "Movies")+" -sample")); n != 2 {
		t.Errorf("in: %d", n)
	}
	eq(t, "modified:2024", f.names(t, "invoice modified:2024"), "invoice-march-2024.pdf")
	eq(t, "before:2021", f.names(t, "invoice before:2021"), "invoice-april.pdf")
	if slices.Contains(f.names(t, "invoice since:2021"), "invoice-april.pdf") {
		t.Error("since:2021")
	}
}

func TestStory9ArabicFolding(t *testing.T) {
	f := newFixture(t)
	eq(t, "ta marbuta", f.names(t, "فاتوره 2024"), "فاتورة ٢٠٢٤.pdf")
	eq(t, "arabic digits", f.names(t, "فاتورة ٢٠٢٤"), "فاتورة ٢٠٢٤.pdf")
	eq(t, "hamza", f.names(t, "احمد"), "أحمد.txt")
	eq(t, "hamza below", f.names(t, "إحمد"), "أحمد.txt")
	eq(t, "cafe", f.names(t, "cafe"), "Café menu.txt")
	eq(t, "CAFÉ", f.names(t, "CAFÉ"), "Café menu.txt")
}

func TestRawFilter(t *testing.T) {
	f := newFixture(t)
	eq(t, "raw:true", f.names(t, "raw:true"), "DSC_0001.NEF")
}

func TestOrNegationPhrase(t *testing.T) {
	f := newFixture(t)
	eq(t, "OR", sorted(f.names(t, "ext:mp4 OR sample")), "Holiday.mp4", "sample.mkv")
	eq(t, "phrase", f.names(t, `"tax return" -draft`), "tax return final.pdf")
}

func TestRelevancePutsBetterMatchesFirst(t *testing.T) {
	f := newFixture(t)
	got := f.names(t, "report")
	if len(got) == 0 || got[len(got)-1] != "old-report.txt" {
		t.Errorf("%q", got)
	}
}

func TestSortingAndPagination(t *testing.T) {
	f := newFixture(t)
	opts := func(srt index.Sort, offset, limit int) SearchOptions {
		return SearchOptions{Page: index.Page{Sort: srt, Offset: offset, Limit: limit}, TypoFallback: true}
	}
	r, _ := f.ld.Search("kind:video", opts(index.BySize, 0, 10))
	var got []string
	for _, h := range r.Hits {
		got = append(got, h.Name)
	}
	eq(t, "by size", got, "Big Trip 2024.mkv", "Holiday.mp4", "sample.mkv")
	r, _ = f.ld.Search("kind:video", opts(index.BySize, 1, 1))
	if r.Total != 3 || r.Hits[0].Name != "Holiday.mp4" {
		t.Errorf("%+v", r)
	}
}

func TestIgnoreRulesAndOwnIndex(t *testing.T) {
	f := newFixture(t)
	eq(t, "node_modules is ignored", f.names(t, "left-pad"))
	eq(t, ".git is listed", f.names(t, `re:^\.git$`), ".git")
	eq(t, ".git contents are not indexed", f.names(t, "cdef"))
	eq(t, "the index never indexes itself", f.names(t, "index.db"))
	eq(t, "hidden:true", f.names(t, "settings hidden:true"), "settings.toml")
	eq(t, "hidden:false", f.names(t, "settings hidden:false"))
}

func TestPositionedErrors(t *testing.T) {
	f := newFixture(t)
	_, err := f.ld.Search("kind:video in:/nowhere/at/all", DefaultSearchOptions)
	e, ok := IsQueryError(err)
	if !ok || e.Span.Start != 11 || e.Span.End != 29 || !strings.Contains(e.Message, "not an indexed folder") {
		t.Errorf("%v", err)
	}
	_, err = f.ld.Search("res:4l", DefaultSearchOptions)
	if e, ok := IsQueryError(err); !ok || e.Suggestion != "res:4k" {
		t.Errorf("%v", err)
	}
}

func TestUnknownFilterKeyWarnsAndSearchesText(t *testing.T) {
	f := newFixture(t)
	r := f.search(t, "rez:4k")
	if len(r.Hits) != 0 || len(r.Warnings) == 0 || r.Warnings[0].Suggestion != "res:" {
		t.Errorf("%+v", r)
	}
}

func TestReindexSeesChanges(t *testing.T) {
	f := newFixture(t)
	write(t, f.root, "Pictures/new.png", "png")
	if err := os.Rename(filepath.Join(f.root, "Documents/old-report.txt"), filepath.Join(f.root, "Documents/renamed-report.txt")); err != nil {
		t.Fatal(err)
	}
	st, err := f.ld.Index(f.root)
	if err != nil || !st.Incremental {
		t.Fatalf("%+v %v", st, err)
	}
	eq(t, "new", f.names(t, "new"), "new.png")
	eq(t, "renamed", f.names(t, "renamed"), "renamed-report.txt")
	eq(t, "old-report", f.names(t, "old-report"))
}

func TestIndexPersistsAcrossOpens(t *testing.T) {
	f := newFixture(t)
	before := sorted(f.names(t, "kind:image"))
	f.ld.Close()
	ld, err := Open(f.db)
	if err != nil {
		t.Fatal(err)
	}
	f.ld = ld
	eq(t, "reopened", sorted(f.names(t, "kind:image")), before...)
	if !reflect.DeepEqual(ld.Stats().Roots, []string{f.root}) {
		t.Errorf("%v", ld.Stats().Roots)
	}
}

func TestIndexingASubfolderRefreshesIt(t *testing.T) {
	f := newFixture(t)
	write(t, f.root, "Movies/extra.mkv", "x")
	st, err := f.ld.Index(filepath.Join(f.root, "Movies"))
	if err != nil {
		t.Fatal(err)
	}
	if !st.Incremental || st.Root != filepath.Join(f.root, "Movies") || st.Added != 1 {
		t.Errorf("%+v", st)
	}
	eq(t, "extra", f.names(t, "extra"), "extra.mkv")
	if n := len(f.ld.Stats().Roots); n != 1 {
		t.Errorf("%d roots", n)
	}
}

func TestForgetAndRefresh(t *testing.T) {
	f := newFixture(t)
	write(t, f.root, "Documents/later.txt", "x")
	u, err := f.ld.Refresh()
	if err != nil || u.Added != 1 {
		t.Fatalf("%+v %v", u, err)
	}
	eq(t, "later", f.names(t, "later"), "later.txt")
	if ok, err := f.ld.Forget(f.root); !ok || err != nil {
		t.Fatal(ok, err)
	}
	eq(t, "forgotten", f.names(t, "invoice"))
}
