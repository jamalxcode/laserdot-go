package core

import (
	"path/filepath"
	"testing"
)

// Indexing the home folder skips ~/Library (app data and caches) except
// iCloud Drive, so the default scope stays the user's own files.
func TestHomeSkipsLibraryButKeepsICloudDrive(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir()) // macOS: /var → /private/var
	home := filepath.Join(dir, "home")
	t.Setenv("HOME", home)
	for _, rel := range []string{
		"Documents/plan.txt",
		"Library/Caches/com.example/plan-cache.txt",
		"Library/Preferences/plan.plist",
		"Library/Mobile Documents/com~apple~CloudDocs/plan-icloud.txt",
	} {
		write(t, home, rel, "x")
	}
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ld.Close() })
	if _, err := ld.Index(home); err != nil {
		t.Fatal(err)
	}
	r, err := ld.Search("plan", DefaultSearchOptions)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, h := range r.Hits {
		names = append(names, h.Name)
	}
	eq(t, "plan", sorted(names), "plan-icloud.txt", "plan.txt")

	// The same rule holds for changes picked up later.
	write(t, home, "Library/Caches/com.example/plan-new.txt", "x")
	if _, err := ld.ApplyChanges([]Change{{Path: filepath.Join(home, "Library"), Recursive: true}}); err != nil {
		t.Fatal(err)
	}
	if r, _ := ld.Search("plan-new", DefaultSearchOptions); r.Total != 0 {
		t.Errorf("a cache file was indexed")
	}
}

// An excluded folder is never indexed (and leaves the index if it was);
// including it again brings it back.
func TestExcludedFolders(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(dir, "home")
	write(t, home, "Documents/plan.txt", "x")
	write(t, home, "Private/plan-secret.txt", "x")
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	if _, err := ld.Index(home); err != nil {
		t.Fatal(err)
	}
	count := func(q string) uint64 {
		r, err := ld.Search(q, DefaultSearchOptions)
		if err != nil {
			t.Fatal(err)
		}
		return r.Total
	}
	if count("plan-secret") != 1 {
		t.Fatal("setup")
	}
	if list, err := ld.Exclude(filepath.Join(home, "Private")); err != nil || len(list) != 1 {
		t.Fatalf("exclude: %v %v", list, err)
	}
	if n := count("plan-secret"); n != 0 {
		t.Errorf("excluded folder still searchable: %d", n)
	}
	if n := count("private"); n != 0 {
		t.Errorf("the excluded folder itself is still listed: %d", n)
	}
	// Changes inside it are not picked up, and a reopen remembers it.
	write(t, home, "Private/plan-new.txt", "x")
	ld.ApplyChanges([]Change{{Path: home, Recursive: true}})
	if count("plan-new") != 0 {
		t.Error("a new file in the excluded folder was indexed")
	}
	if got := ld.Exclusions(); len(got) != 1 {
		t.Errorf("exclusions: %v", got)
	}
	if _, err := ld.Include(filepath.Join(home, "Private")); err != nil {
		t.Fatal(err)
	}
	if count("plan-secret") != 1 || count("plan-new") != 1 {
		t.Error("included again, but not indexed")
	}
}
