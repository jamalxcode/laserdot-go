package query

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

const now = 1_762_732_800 // 2025-11-10T00:00:00Z

func p(t *testing.T, q string) *Query {
	t.Helper()
	r, err := ParseAt(q, now)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return r
}

func perr(t *testing.T, q string) *Error {
	t.Helper()
	_, err := ParseAt(q, now)
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("%s should fail with a positioned error, got %v", q, err)
	}
	return e
}

func filter(t *testing.T, q string) Filter {
	t.Helper()
	term := p(t, q).Clauses[0].Terms[0]
	if term.Kind != TermFilter {
		t.Fatalf("%s: not a filter", q)
	}
	return term.Filter
}

func TestWordsAndClauses(t *testing.T) {
	q := p(t, "Invoice  march")
	if len(q.Clauses) != 2 || q.Clauses[0].Terms[0].Text.Text != "invoice" {
		t.Fatalf("%+v", q)
	}
	if got := q.Clauses[1].Terms[0].Span; got != (Span{9, 14}) {
		t.Errorf("span %v", got)
	}
}

func TestOrNegationPhrase(t *testing.T) {
	q := p(t, `mkv OR mp4 -sample "tax return"`)
	if len(q.Clauses) != 3 || len(q.Clauses[0].Terms) != 2 || !q.Clauses[1].Terms[0].Negated {
		t.Fatalf("%+v", q)
	}
	if tv := q.Clauses[2].Terms[0].Text; !tv.Phrase || tv.Text != "tax return" {
		t.Errorf("phrase %+v", tv)
	}
	for _, s := range []string{"OR mkv", "mkv OR"} {
		if !strings.Contains(perr(t, s).Message, "OR") {
			t.Errorf("%s", s)
		}
	}
}

func TestWildcardsDetected(t *testing.T) {
	q := p(t, "report* file?.txt report(2).pdf")
	var got []bool
	for _, c := range q.Clauses {
		got = append(got, c.Terms[0].Text.Wildcards)
	}
	if !reflect.DeepEqual(got, []bool{true, true, false}) {
		t.Errorf("%v", got)
	}
}

func TestUnknownKeyIsTextWithWarning(t *testing.T) {
	q := p(t, "rez:4k 10:30.mp4")
	if q.Clauses[0].Terms[0].Kind != TermText || q.Clauses[1].Terms[0].Kind != TermText {
		t.Fatal("not text")
	}
	if len(q.Warnings) != 1 || q.Warnings[0].Suggestion != "res:" {
		t.Errorf("%+v", q.Warnings)
	}
}

func iv(t *testing.T, q string) Interval {
	t.Helper()
	return filter(t, q).Value.Interval
}

func TestSizes(t *testing.T) {
	inf := math.Inf(1)
	cases := map[string]Interval{
		"fsize:>4gb":     {math.Nextafter(4e9, inf), inf},
		"fsize:<=4gb":    {math.Inf(-1), math.Nextafter(4e9, inf)},
		"modified:>2024": {1_735_689_600, inf},
		"fsize:>=4gb":    {4e9, inf},
		"fsize:1gb..4gb": {1e9, 5e9},
	}
	for q, want := range cases {
		if got := iv(t, q); got != want {
			t.Errorf("%s: %v, want %v", q, got, want)
		}
	}
	got := iv(t, "fsize:4.5GB")
	if math.Abs(got.Lo-4.5e9) > 1 || math.Abs(got.Hi-4.6e9) > 1 {
		t.Errorf("4.5GB: %v", got)
	}
	if !strings.Contains(perr(t, "fsize:huge").Message, "sizes look like") {
		t.Error("huge")
	}
	if !strings.Contains(perr(t, "fsize:4zb").Message, "unit") {
		t.Error("zb")
	}
}

func TestArabicDigitsInValues(t *testing.T) {
	if iv(t, "fsize:>٤gb") != iv(t, "fsize:>4gb") {
		t.Error("arabic digits")
	}
}

func TestDates(t *testing.T) {
	if got := iv(t, "modified:2024"); got != (Interval{1_704_067_200, 1_735_689_600}) {
		t.Errorf("year %v", got)
	}
	if got := iv(t, "modified:2024-02"); got != (Interval{1_706_745_600, 1_709_251_200}) {
		t.Errorf("month %v", got)
	}
	if got := iv(t, "since:7d"); got != (Interval{float64(now - 7*86_400), math.Inf(1)}) {
		t.Errorf("relative %v", got)
	}
	if !strings.Contains(perr(t, "modified:2024-13").Message, "month") {
		t.Error("month 13")
	}
	if !strings.Contains(perr(t, "modified:2023-02-29").Message, "real date") {
		t.Error("feb 29")
	}
	if perr(t, "since:>2024").Suggestion != "modified:>2024" {
		t.Error("since suggestion")
	}
}

func TestEnumsAndBools(t *testing.T) {
	if got := filter(t, "kind:video,audio").Value.Enum; !reflect.DeepEqual(got, []string{"video", "audio"}) {
		t.Errorf("%v", got)
	}
	if got := filter(t, "res:4K").Value.Enum; !reflect.DeepEqual(got, []string{"4k"}) {
		t.Errorf("%v", got)
	}
	e := perr(t, "res:4l")
	if e.Suggestion != "res:4k" || e.Span != (Span{4, 6}) {
		t.Errorf("%+v", e)
	}
	if !filter(t, "local:yes").Value.Bool {
		t.Error("yes")
	}
	if !strings.Contains(perr(t, "local:maybe").Message, "true or false") {
		t.Error("maybe")
	}
}

func TestTextListsAndPhrases(t *testing.T) {
	if n := len(filter(t, "ext:mkv,MP4").Value.Text); n != 2 {
		t.Errorf("%d items", n)
	}
	v := filter(t, `artist:"Fairouz, Ziad"`).Value.Text
	if len(v) != 1 || !v[0].Phrase || v[0].Text != "fairouz, ziad" {
		t.Errorf("%+v", v)
	}
}

func TestTrackScopeAndGroups(t *testing.T) {
	if filter(t, "a.lang:ar").Scope != Audio || filter(t, "s.lang:en").Scope != Subtitle {
		t.Error("scopes")
	}
	if !strings.Contains(perr(t, "s.codec:hevc").Message, "subtitle") {
		t.Error("s.codec")
	}
	q := p(t, "a(lang:ar channels:5.1) res:4k")
	g := q.Clauses[0].Terms[0]
	if g.Kind != TermGroup || g.Scope != Audio || len(g.Group) != 2 || g.Group[1].Span != (Span{10, 22}) {
		t.Errorf("%+v", g)
	}
	if !strings.Contains(perr(t, "a(lang:ar").Message, "unclosed") {
		t.Error("unclosed group")
	}
	if !strings.Contains(perr(t, "a(arabic)").Message, "only filters") {
		t.Error("word in group")
	}
}

func TestRawRegex(t *testing.T) {
	q := p(t, `kind:image re:^IMG_\d{4}\.heic$`)
	if len(q.Clauses) != 1 || q.Raw == nil || q.Raw.Regex != `^IMG_\d{4}\.heic$` || q.Raw.Span.Start != 14 {
		t.Fatalf("%+v %+v", q, q.Raw)
	}
	if !strings.Contains(perr(t, "re:").Message, "regex") {
		t.Error("empty re:")
	}
}

func TestUnclosedQuote(t *testing.T) {
	if perr(t, `"tax return`).Span.Start != 0 {
		t.Error("span")
	}
}

func TestEmptyQuery(t *testing.T) {
	if !p(t, "   ").IsEmpty() {
		t.Error("not empty")
	}
}

// Any input yields a query or a positioned error inside the input, never a
// panic (spec §15: parser property tests).
func FuzzParse(f *testing.F) {
	for _, s := range []string{"invoice march", "a(lang:ar channels:5.1) res:4k", `"x`, "re:(", "fsize:1..", "-", "OR", "a(", "kind:", "since:>2024", "فاتورة ٢٠٢٤"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8.ValidString(s) {
			return
		}
		_, err := ParseAt(s, now)
		if err == nil {
			return
		}
		var e *Error
		if !errors.As(err, &e) {
			t.Fatalf("%q: error without a position: %v", s, err)
		}
		n := utf8.RuneCountInString(s)
		if e.Span.Start < 0 || e.Span.End > n || e.Span.Start > e.Span.End {
			t.Fatalf("%q: span %v outside 0..%d", s, e.Span, n)
		}
	})
}
