package core

import (
	"fmt"
	"path/filepath"
	"strings"
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

// Storage lists a folder's entries by total size, from the index.
func TestStorage(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(dir, "home")
	write(t, root, "Movies/a.mkv", strings.Repeat("x", 5000))
	write(t, root, "Movies/b.mkv", strings.Repeat("x", 3000))
	write(t, root, "Docs/n.txt", strings.Repeat("x", 100))
	write(t, root, "big.iso", strings.Repeat("x", 6000))
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	rep, err := ld.Storage(root, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range rep.Items {
		got = append(got, fmt.Sprintf("%s=%d/%d", it.Name, it.Bytes, it.Files))
	}
	if want := []string{"Movies=8000/2", "big.iso=6000/1", "Docs=100/1"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %v, want %v", got, want)
	}
	if rep.Bytes != 14100 || rep.Files != 4 {
		t.Errorf("total %d/%d", rep.Bytes, rep.Files)
	}
	top, _ := ld.Storage("", 10)
	if len(top.Items) != 1 || top.Items[0].Bytes != 14100 {
		t.Errorf("roots: %+v", top.Items)
	}
}

// Apps and other packages are one entry in the index, with nothing inside;
// Storage measures them from the disk so they count at their real size, and
// counts them as apps, not files (the owner saw /Applications at 25 KB).
func TestStorageCountsAppsAtTheirRealSize(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	apps := filepath.Join(dir, "Applications")
	write(t, apps, "Big.app/Contents/MacOS/Big", strings.Repeat("x", 70000))
	write(t, apps, "Big.app/Contents/Resources/art.bin", strings.Repeat("x", 30000))
	write(t, apps, "Small.app/Contents/Info.plist", strings.Repeat("x", 500))
	write(t, apps, ".localized", "")
	write(t, apps, "Tools/Helper.app/Contents/MacOS/Helper", strings.Repeat("x", 2000))
	ld, err := Open(filepath.Join(dir, "data", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	if _, err := ld.Index(apps); err != nil {
		t.Fatal(err)
	}
	top, err := ld.Storage("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(top.Items) != 1 {
		t.Fatalf("roots: %+v", top.Items)
	}
	it := top.Items[0]
	if it.Bytes != 102500 || it.Apps != 3 || it.Files != 1 || it.Partial {
		t.Errorf("Applications: %d bytes, %d apps, %d files (partial %v); want 102500, 3 apps, 1 file", it.Bytes, it.Apps, it.Files, it.Partial)
	}
	inside, _ := ld.Storage(apps, 10)
	if inside.Items[0].Name != "Big.app" || inside.Items[0].Bytes != 100000 || inside.Items[0].Apps != 1 {
		t.Errorf("inside: %+v", inside.Items)
	}
	// Measured once: the next request answers from memory, even with no
	// time to measure at all.
	old := StorageBudget
	StorageBudget = 0
	defer func() { StorageBudget = old }()
	again, _ := ld.Storage("", 10)
	if again.Items[0].Bytes != 102500 || again.Partial {
		t.Errorf("remembered: %+v", again.Items[0])
	}
	// Not measured in time: said so ("at least"), never shown as final.
	ld.pkgSizes = packageSizes{}
	short, _ := ld.Storage("", 10)
	if !short.Partial || !short.Items[0].Partial {
		t.Errorf("an unmeasured package is marked partial: %+v", short.Items[0])
	}
}
