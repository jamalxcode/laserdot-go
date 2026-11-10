package core

// Content search at Seam 1 (§2.4, story 7): real files, the public API,
// results out.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func put(t *testing.T, p string, data []byte, age time.Duration) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	past(t, p, age)
}

// onePagePDF shows text in Helvetica.
func onePagePDF(text string) []byte {
	stream := fmt.Sprintf("BT /F1 24 Tf 72 700 Td (%s) Tj ET", text)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
	}
	out := []byte("%PDF-1.4\n")
	var offsets []int
	for i, o := range objects {
		offsets = append(offsets, len(out))
		out = append(out, fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, o)...)
	}
	xref := len(out)
	out = append(out, fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)...)
	for _, off := range offsets {
		out = append(out, fmt.Sprintf("%010d 00000 n \n", off)...)
	}
	return append(out, fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)...)
}

type lib struct {
	root string
	ld   *Laserdot
}

func library(t *testing.T) *lib {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "lib")
	docs := filepath.Join(root, "Documents")
	h := time.Hour
	put(t, filepath.Join(docs, "notes.txt"), []byte("Meeting about the quarterly BUDGET with Fairouz.\n"), h)
	put(t, filepath.Join(docs, "budget-2024.txt"), []byte("numbers only: 1 2 3\n"), h)
	put(t, filepath.Join(docs, "code/main.rs"), []byte("fn compute_invoice_total() -> u64 { 42 }\n"), h)
	put(t, filepath.Join(docs, "arabic.md"), []byte("فاتورة الكهرباء لشهر مارس"), h)
	put(t, filepath.Join(docs, "report.pdf"), onePagePDF("Confidential merger plans"), h)
	put(t, filepath.Join(docs, "data.txt"), append([]byte("secretword\x00\x00\x01\x02"), make([]byte, 64)...), h)
	put(t, filepath.Join(docs, "huge.log"), []byte(strings.Repeat("hugeword ", 300)), h)
	wide := []byte{0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune("unicodeword in UTF-16")) {
		wide = append(wide, byte(u), byte(u>>8))
	}
	put(t, filepath.Join(docs, "wide.txt"), wide, h)
	put(t, filepath.Join(root, "Other/outside.txt"), []byte("budget outside the content folders\n"), h)
	ld, err := Open(filepath.Join(base, "data/index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ld.Close() })
	if err := ld.SetContentSettings(ContentSettings{Roots: []string{docs}, MaxBytes: 1000}); err != nil {
		t.Fatal(err)
	}
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	s, err := ld.UpdateContent()
	// notes, budget-2024, main.rs, arabic.md, report.pdf, wide.txt; the
	// binary and the over-size file are skipped.
	if err != nil || s.Indexed != 6 || s.Skipped != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	return &lib{root, ld}
}

type nameContent struct {
	name    string
	content bool
}

func find(t *testing.T, ld *Laserdot, q string) []nameContent {
	t.Helper()
	r, err := ld.Search(q, DefaultSearchOptions)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	var out []nameContent
	for _, h := range r.Hits {
		out = append(out, nameContent{h.Name, h.Content})
	}
	return out
}

func cnames(t *testing.T, ld *Laserdot, q string) []string {
	t.Helper()
	out := []string{}
	for _, nc := range find(t, ld, q) {
		out = append(out, nc.name)
	}
	slices.Sort(out)
	return out
}

func expect(t *testing.T, got []string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStory7WordsMatchContentsAfterNames(t *testing.T) {
	l := library(t)
	if got := find(t, l.ld, "budget"); !reflect.DeepEqual(got, []nameContent{{"budget-2024.txt", false}, {"notes.txt", true}}) {
		t.Errorf("%+v", got)
	}
	expect(t, cnames(t, l.ld, "quart"), "notes.txt")
	expect(t, cnames(t, l.ld, "quarterly fairouz"), "notes.txt")
	expect(t, cnames(t, l.ld, "quarterly merger"))
	expect(t, cnames(t, l.ld, "invoice"), "main.rs")
	expect(t, cnames(t, l.ld, "merger"), "report.pdf")
	expect(t, cnames(t, l.ld, "unicodeword"), "wide.txt")
	expect(t, cnames(t, l.ld, "فاتوره"), "arabic.md")
	expect(t, cnames(t, l.ld, "الكهرباء"), "arabic.md")
}

func TestStory7FiltersApplyToContentMatches(t *testing.T) {
	l := library(t)
	expect(t, cnames(t, l.ld, "budget ext:txt"), "budget-2024.txt", "notes.txt")
	expect(t, cnames(t, l.ld, "budget ext:md"))
	expect(t, cnames(t, l.ld, "budget -notes"), "budget-2024.txt")
	expect(t, cnames(t, l.ld, "invoice in:"+filepath.Join(l.root, "Documents/code")), "main.rs")
	// Wildcard words and re: are name patterns only.
	expect(t, cnames(t, l.ld, "quart*"))
}

func TestContentFilter(t *testing.T) {
	l := library(t)
	expect(t, cnames(t, l.ld, "content:merger"), "report.pdf")
	expect(t, cnames(t, l.ld, `content:"merger plans"`), "report.pdf")
	expect(t, cnames(t, l.ld, `content:"plans merger"`))
	expect(t, cnames(t, l.ld, "content:merg*"), "report.pdf")
	expect(t, cnames(t, l.ld, "content:merger,invoice"), "main.rs", "report.pdf")
	expect(t, cnames(t, l.ld, "notes content:budget"), "notes.txt")
	expect(t, cnames(t, l.ld, "notes content:merger"))
	// Negated (outside.txt mentions budget, but is not content-indexed).
	expect(t, cnames(t, l.ld, "ext:txt -content:budget"), "budget-2024.txt", "data.txt", "outside.txt", "wide.txt")
	for q, start := range map[string]int{"content:m*r": 0, "x content:a OR y": 2} {
		_, err := l.ld.Search(q, DefaultSearchOptions)
		if e, ok := IsQueryError(err); !ok || e.Span.Start != start {
			t.Errorf("%s: %v", q, err)
		}
	}
}

func TestSkippedAndOutOfScopeFiles(t *testing.T) {
	l := library(t)
	expect(t, cnames(t, l.ld, "secretword"))
	expect(t, cnames(t, l.ld, "hugeword"))
	expect(t, cnames(t, l.ld, "budget outside"))
	st, _ := l.ld.ContentStatus()
	if st.Documents != 6 || st.Skipped != 2 || st.Pending != 0 {
		t.Errorf("%+v", st)
	}
}

func TestContentIncrementalUpdates(t *testing.T) {
	l := library(t)
	if s, _ := l.ld.UpdateContent(); s.Indexed != 0 {
		t.Error("nothing changed: nothing read", s)
	}
	put(t, filepath.Join(l.root, "Documents/notes.txt"), []byte("Annual summary.\n"), time.Minute)
	l.ld.Refresh()
	if s, _ := l.ld.UpdateContent(); s.Indexed != 1 {
		t.Error(s)
	}
	expect(t, cnames(t, l.ld, "quarterly"))
	expect(t, cnames(t, l.ld, "annual"), "notes.txt")
	os.Remove(filepath.Join(l.root, "Documents/report.pdf"))
	l.ld.Refresh()
	if s, _ := l.ld.UpdateContent(); s.Removed != 1 {
		t.Error(s)
	}
	expect(t, cnames(t, l.ld, "merger"))
	l.ld.SetContentSettings(ContentSettings{Roots: []string{filepath.Join(l.root, "Documents/code")}, MaxBytes: 1000})
	if s, _ := l.ld.UpdateContent(); s.Indexed != 0 {
		t.Error(s)
	}
	expect(t, cnames(t, l.ld, "annual"))
	expect(t, cnames(t, l.ld, "invoice"), "main.rs")
}
