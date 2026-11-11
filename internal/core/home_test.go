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
