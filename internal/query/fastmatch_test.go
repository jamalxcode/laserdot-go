package query

import (
	"math/rand"
	"testing"
)

// The fast paths (a plain word by substring search; a whole-name pattern)
// give exactly the regex's answer and quality, for any name.
func TestFastMatchAgreesWithTheRegex(t *testing.T) {
	words := []string{"report", "invoice", "img", "2024", "فاتوره", "cafe", "a", "report*", "*port", "rep?rt", `"report"`,
		"r*t", "*e*", "re*po*rt", "*", "**", "a*a", "rep*rep"}
	alphabet := []rune("reportinvcafeg2041 -_.فاتوره")
	rng := rand.New(rand.NewSource(7))
	names := []string{"report", "report.pdf", "my report", "reportage", "xreport", "", "img_2024.jpg", "فاتوره ٢٠٢٤", "café",
		"rep", "reprep", "repxrep", "aa", "a", "rt", "r t", "e"}
	for i := 0; i < 3000; i++ {
		n := rng.Intn(14)
		r := make([]rune, n)
		for j := range r {
			r[j] = alphabet[rng.Intn(len(alphabet))]
		}
		names = append(names, string(r))
	}
	fast := 0
	for _, w := range words {
		p, err := Parse(w)
		if err != nil {
			t.Fatal(err)
		}
		c, err := Compile(p, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, cl := range c.Clauses {
			for _, term := range cl.Terms {
				m := term.Name
				if m == nil {
					continue
				}
				if m.Literal != "" || m.Whole || m.Star != nil {
					fast++
				}
				slow := &NameMatcher{Regex: m.Regex, Typo: m.Typo}
				for _, name := range names {
					if m.Match(name) != slow.Match(name) {
						t.Fatalf("%s on %q: match %v, regex %v", w, name, m.Match(name), slow.Match(name))
					}
					if m.Quality(name) != slow.Quality(name) {
						t.Fatalf("%s on %q: quality %v, regex %v", w, name, m.Quality(name), slow.Quality(name))
					}
				}
			}
		}
	}
	if fast < 8 {
		t.Errorf("the fast paths were taken for only %d patterns", fast)
	}
}
