package core

import (
	"path/filepath"
	"testing"
)

// Results the user opened come first among equally good matches, and the
// history survives a restart (spec §6.2).
func TestOpenedResultsRankFirst(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "home")
	for _, rel := range []string{"report-a.pdf", "report-b.pdf", "report-c.pdf", "notes/report.txt"} {
		write(t, root, rel, "x")
	}
	db := filepath.Join(dir, "index.db")
	ld, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	first := func(l *Laserdot, q string) string {
		t.Helper()
		r, err := l.Search(q, DefaultSearchOptions)
		if err != nil || len(r.Hits) == 0 {
			t.Fatalf("%s: %v", q, err)
		}
		return r.Hits[0].Name
	}
	if got := first(ld, "report"); got != "report.txt" {
		t.Fatalf("before: %s", got) // the shortest name
	}
	real, _ := filepath.EvalSymlinks(root)
	for range 2 {
		if err := ld.RecordOpen(filepath.Join(real, "report-c.pdf")); err != nil {
			t.Fatal(err)
		}
	}
	if got := first(ld, "report"); got != "report-c.pdf" {
		t.Errorf("after opening: %s", got)
	}
	if got := first(ld, "kind:document"); got != "report-c.pdf" {
		t.Errorf("filter-only after opening: %s", got)
	}
	// A better name match still wins over an opened weaker one.
	if got := first(ld, "report.txt"); got != "report.txt" {
		t.Errorf("exact name: %s", got)
	}
	ld.Close()
	ld2, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer ld2.Close()
	if got := first(ld2, "report"); got != "report-c.pdf" {
		t.Errorf("after restart: %s", got)
	}
	if err := ld2.ClearOpens(); err != nil {
		t.Fatal(err)
	}
	if got := first(ld2, "report"); got != "report.txt" {
		t.Errorf("after clearing: %s", got)
	}
}

// The empty search box lists what was opened last, newest first; files
// that are gone are left out.
func TestRecentOpens(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(dir, "home")
	for _, rel := range []string{"a.txt", "b.txt", "c.txt"} {
		write(t, root, rel, "x")
	}
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := ld.RecordOpen(filepath.Join(root, n)); err != nil {
			t.Fatal(err)
		}
	}
	// Opened in the same second: the order among them is not defined, so
	// open b again later.
	ld.opens.mu.Lock()
	r := ld.opens.records[filepath.Join(root, "b.txt")]
	r.Last += 10
	ld.opens.records[filepath.Join(root, "b.txt")] = r
	ld.opens.mu.Unlock()
	got := ld.RecentOpens(2)
	if len(got) != 2 || got[0].Name != "b.txt" {
		t.Errorf("recent: %+v", got)
	}
}

// While a folder is indexed for the first time its progress is reported;
// afterwards there is none.
func TestIndexProgress(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(dir, "home")
	write(t, root, "a.txt", "x")
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	if ld.Progress() != nil {
		t.Fatal("progress before indexing")
	}
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	if ld.Progress() != nil {
		t.Error("progress left after indexing")
	}
}
