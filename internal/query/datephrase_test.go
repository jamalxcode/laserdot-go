package query

import (
	"testing"
	"time"
)

// The owner chose "more date words": seasons, a month with its year, and a
// span of months, in English and Arabic. Each becomes one modified: filter.
func TestDatePhrases(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC).Unix()
	for in, want := range map[string]string{
		"march 2024":                   "modified:2024-03",
		"Mar 2024":                     "modified:2024-03",
		"مارس 2024":                    "modified:2024-03",
		"summer 2023":                  "modified:2023-06..2023-08",
		"صيف 2023":                     "modified:2023-06..2023-08",
		"last summer":                  "modified:2026-06..2026-08",
		"الصيف الماضي":                 "modified:2026-06..2026-08",
		"last winter":                  "modified:2025-12..2026-02",
		"الشتاء الماضي":                "modified:2025-12..2026-02",
		"this winter":                  "modified:2025-12..2026-02",
		"between march and may":        "modified:2026-03..2026-05",
		"between march and may 2024":   "modified:2024-03..2024-05",
		"from jan to mar":              "modified:2026-01..2026-03",
		"between nov and feb":          "modified:2026-11..2027-02",
		"between dec and feb 2025":     "modified:2024-12..2025-02",
		"بين يناير ومارس":              "modified:2026-01..2026-03",
		"من يناير الى مارس":            "modified:2026-01..2026-03",
		"between december and january": "modified:2025-12..2026-01",
	} {
		words := splitWords(in)
		got, n := PhraseAt(words, now)
		if got != want || n != len(words) {
			t.Errorf("%q: got %q (%d words), want %q", in, got, n, want)
		}
		q, err := ParseAt("report "+in, now)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if len(q.Clauses) != 2 || q.Clauses[1].Terms[0].Kind != TermFilter {
			t.Errorf("%q: not one filter after the word: %+v", in, q.Clauses)
		}
	}
	// A month alone, or a span missing its end, stays words.
	for _, in := range []string{"invoice march", "march madness", "between march", "from home to work", "may be"} {
		if got, n := PhraseAt(splitWords(in), now); n != 0 {
			t.Errorf("%q: should be words, got %q", in, got)
		}
		for i, w := range splitWords(in) {
			if got, n := PhraseAt(splitWords(in)[i:], now); n != 0 {
				t.Errorf("%q at %q: should be words, got %q", in, w, got)
			}
		}
	}
}

func splitWords(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
