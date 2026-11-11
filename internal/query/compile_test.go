package query

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func comp(t *testing.T, q string) *Compiled {
	t.Helper()
	pq, err := ParseAt(q, 0)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	c, err := Compile(pq, false)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return c
}

func name(t *testing.T, q string) *NameMatcher {
	t.Helper()
	return comp(t, q).Clauses[0].Terms[0].Name
}

func TestPlainWordIsSubstring(t *testing.T) {
	m := name(t, "march")
	if !m.Match("invoice-march-2024.pdf") || m.Match("mar.pdf") {
		t.Error("substring")
	}
	if !reflect.DeepEqual(m.Prefilter, [][]string{{"march"}}) {
		t.Errorf("%v", m.Prefilter)
	}
}

func TestSpecialCharsAreLiteral(t *testing.T) {
	m := name(t, "report(2).pdf")
	if !m.Match("report(2).pdf") || m.Match("report2xpdf") {
		t.Error("parens and dot")
	}
	if !name(t, "a+b[1]").Match("a+b[1].txt") {
		t.Error("plus and brackets")
	}
}

func TestWildcardsAnchorWholeName(t *testing.T) {
	m := name(t, "report*")
	if !m.Match("report-final.pdf") || m.Match("old-report.pdf") {
		t.Error("report*")
	}
	m = name(t, "file?.txt")
	if !m.Match("file1.txt") || m.Match("file12.txt") || m.Match("myfile1.txt") {
		t.Error("file?.txt")
	}
	if !reflect.DeepEqual(m.Prefilter, [][]string{{"file"}, {".txt"}}) {
		t.Errorf("%v", m.Prefilter)
	}
	if !name(t, "*2024*").Match("taxes 2024 final") {
		t.Error("*2024*")
	}
}

func TestWildcardQuestionMatchesOneUnicodeChar(t *testing.T) {
	if !name(t, "ملف?.txt").Match("ملفأ.txt") {
		t.Error("? over an Arabic letter")
	}
}

func TestQualityRanking(t *testing.T) {
	m := name(t, "invoice")
	for s, want := range map[string]MatchQuality{
		"invoice": Exact, "invoice-03.pdf": Prefix, "march invoice.pdf": WordStart,
		"reinvoiced.pdf": Substring, "receipt.pdf": NoMatch,
	} {
		if got := m.Quality(s); got != want {
			t.Errorf("%s: %v, want %v", s, got, want)
		}
	}
}

func TestTypoVariants(t *testing.T) {
	q, _ := ParseAt("inovice", 0)
	if !HasTypoCandidates(q) {
		t.Fatal("candidate")
	}
	c, err := Compile(q, true)
	if err != nil {
		t.Fatal(err)
	}
	m := c.Clauses[0].Terms[0].Name
	if !m.Typo {
		t.Error("typo flag")
	}
	for _, n := range []string{"invoice.pdf", "inovice.pdf", "inavice.pdf", "invice.pdf"} {
		if !m.Match(n) {
			t.Errorf("%s", n)
		}
	}
	if m.Match("receipt.pdf") {
		t.Error("receipt")
	}
	found := false
	for _, p := range m.Prefilter[0] {
		found = found || p == "inovice"
	}
	if !found {
		t.Error("prefilter piece")
	}
	for _, s := range []string{"ta", "2024", "report(2).pdf", strings.Repeat("abcdefghij", 5)} {
		q, _ := ParseAt(s, 0)
		if HasTypoCandidates(q) {
			t.Errorf("%s should not get typo variants", s)
		}
	}
}

func TestExtIsExact(t *testing.T) {
	ms := comp(t, "ext:mkv").Clauses[0].Terms[0].Filter.Value.Text
	if !ms[0].Match("mkv") || ms[0].Match("mkv2") {
		t.Error("ext exact")
	}
}

func TestRawRegexCaseInsensitiveAndPositionedErrors(t *testing.T) {
	m := comp(t, `re:^IMG_\d{4}\.heic$`).Clauses[0].Terms[0].Name
	if !m.Match("img_1234.heic") {
		t.Error("case-insensitive")
	}
	if !reflect.DeepEqual(m.Prefilter, [][]string{{"img_"}, {".heic"}}) {
		t.Errorf("%v", m.Prefilter)
	}
	q, _ := ParseAt("re:abc(", 0)
	_, err := Compile(q, false)
	var e *Error
	if !errors.As(err, &e) || !strings.HasPrefix(e.Message, "invalid regex") || e.Span.Start < 3 {
		t.Errorf("%v", err)
	}
}

func TestGroups(t *testing.T) {
	if g := comp(t, "a(lang:ar channels:5.1)").Clauses[0].Terms[0]; g.Kind != CGroup || len(g.Group) != 2 {
		t.Errorf("%+v", g)
	}
	if _, err := ParseAt("a(lang:ar fsize:>1gb)", 0); err == nil || !strings.Contains(err.Error(), "whole file") {
		t.Errorf("%v", err)
	}
	q, _ := ParseAt("a(count:2)", 0)
	if _, err := Compile(q, false); err == nil || !strings.Contains(err.Error(), "whole file") {
		t.Errorf("%v", err)
	}
}

// Whatever the parser accepts compiles (or fails with a positioned error).
func FuzzCompile(f *testing.F) {
	for _, s := range []string{"invoice march", "re:a(b", "re:[", "ملف?.txt", "*", "a(lang:ar)", "re:(?i)x{2,}"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q, err := ParseAt(s, 0)
		if err != nil {
			return
		}
		for _, typo := range []bool{false, true} {
			if _, err := Compile(q, typo); err != nil {
				var e *Error
				if !errors.As(err, &e) {
					t.Fatalf("%q: unpositioned error %v", s, err)
				}
			}
		}
	})
}

// Three-letter words forgive swapped neighbours only (pfd → pdf), not any
// edit (tax must not find fax).
func TestShortWordsForgiveSwapsOnly(t *testing.T) {
	q, _ := ParseAt("pfd", 0)
	c, err := Compile(q, true)
	if err != nil {
		t.Fatal(err)
	}
	m := c.Clauses[0].Terms[0].Name
	if !m.Match("report.pdf") || m.Match("pad.txt") {
		t.Error("pfd should find pdf only by a swap")
	}
	q, _ = ParseAt("tax", 0)
	c, _ = Compile(q, true)
	if n := c.Clauses[0].Terms[0].Name; n.Match("fax.txt") || !n.Match("atx.txt") {
		t.Error("tax: swaps only")
	}
}
