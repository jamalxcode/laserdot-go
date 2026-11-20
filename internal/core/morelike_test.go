package core

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The owner chose "more like this": from a result, a search for files of
// the same type, a similar size and a near date. It is query language, so
// it shows in the search field and can be changed.
func TestMoreLikeThisFindsSimilarFiles(t *testing.T) {
	ld, root := newCore(t)
	put := func(name string, size int, ago time.Duration) {
		p := filepath.Join(root, name)
		writeAged(t, p, bytes.Repeat([]byte("x"), size))
		past(t, p, ago)
	}
	day := 24 * time.Hour
	put("scans/receipt.pdf", 300_000, 100*day)
	put("scans/other receipt.pdf", 400_000, 110*day) // like it
	put("work/invoice.pdf", 200_000, 95*day)         // like it, another folder
	put("work/huge.pdf", 5_000_000, 100*day)         // too big
	put("work/old.pdf", 300_000, 400*day)            // too old
	put("work/receipt.docx", 300_000, 100*day)       // another type
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	q, err := ld.MoreLike(filepath.Join(root, "scans/receipt.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(q, "ext:pdf fsize:") || !strings.Contains(q, "modified:") {
		t.Errorf("the type, size and date as words: %q", q)
	}
	r, err := ld.Search(q, DefaultSearchOptions)
	if err != nil {
		t.Fatalf("%q: %v", q, err)
	}
	var names []string
	for _, h := range r.Hits {
		names = append(names, h.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"invoice.pdf", "other receipt.pdf", "receipt.pdf"}) {
		t.Errorf("%q found %v", q, names)
	}
	// Through a symlinked path (macOS's /var is /private/var).
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(root, "scans"), link); err == nil {
		if q2, err := ld.MoreLike(filepath.Join(link, "receipt.pdf")); err != nil || q2 != q {
			t.Errorf("through a symlink: %q %v", q2, err)
		}
	}
	// A folder: a search inside it. Not indexed: an error.
	if q, _ := ld.MoreLike(filepath.Join(root, "work")); !strings.HasPrefix(q, "in:") {
		t.Errorf("a folder searches inside it: %q", q)
	}
	if _, err := ld.MoreLike(filepath.Join(root, "nope.txt")); err == nil {
		t.Error("a file not in the index")
	}
}
