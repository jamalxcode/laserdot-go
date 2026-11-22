package core

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// Among names that match the words equally well, the one changed more
// recently comes first (the owner: "1 do it" — Spotlight weighs recency): a
// longer name changed this week beats a shorter one from two years ago, but
// a better match still beats a newer worse one.
func TestRecentFilesRankFirstAmongEqualMatches(t *testing.T) {
	ld, root := newCore(t)
	old := filepath.Join(root, "Taxes/march invoice.pdf")
	recent := filepath.Join(root, "Taxes/march invoice final copy.pdf")
	exact := filepath.Join(root, "Old/invoice.pdf")
	for _, p := range []string{old, recent, exact} {
		writeAged(t, p, []byte("x"))
	}
	past(t, old, 2*365*24*time.Hour)
	past(t, exact, 3*365*24*time.Hour)
	past(t, recent, 3*24*time.Hour)
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	r, err := ld.Search("march invoice", DefaultSearchOptions)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, h := range r.Hits {
		names = append(names, h.Name)
	}
	if !slices.Equal(names, []string{"march invoice final copy.pdf", "march invoice.pdf"}) {
		t.Errorf("this week's copy first: %v", names)
	}
	// "invoice" names the old invoice.pdf exactly: the better match wins.
	r, err = ld.Search("invoice", DefaultSearchOptions)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Hits) == 0 || r.Hits[0].Name != "invoice.pdf" {
		t.Errorf("the exact name first: %v", r.Hits)
	}
}
