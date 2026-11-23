package core

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A search's results by month, oldest first; months with none left out.
func TestTimelineCountsResultsByMonth(t *testing.T) {
	ld, root := newCore(t)
	at := func(name string, y int, m time.Month) {
		p := filepath.Join(root, name)
		writeAged(t, p, []byte("x"))
		past(t, p, time.Since(time.Date(y, m, 15, 12, 0, 0, 0, time.Local)))
	}
	at("trip a.jpg", 2024, time.March)
	at("trip b.jpg", 2024, time.March)
	at("trip c.jpg", 2024, time.May)
	at("notes.txt", 2024, time.April) // not a result
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	tl, err := ld.Timeline("trip")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tl, []MonthCount{{"2024-03", 2}, {"2024-05", 1}}) {
		t.Errorf("by month: %v", tl)
	}
	if tl, _ := ld.Timeline(""); len(tl) != 0 {
		t.Errorf("no query, no timeline: %v", tl)
	}
}
