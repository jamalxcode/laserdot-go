package core

import (
	"path/filepath"
	"testing"

	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// Apps are packages: one result each, their contents not indexed — as in
// Finder and Spotlight. The same holds for changes inside them later.
func TestAppsAreOneResult(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "Applications")
	for _, rel := range []string{
		"Claude.app/Contents/Info.plist", "Claude.app/Contents/MacOS/Claude",
		"Claude.app/Contents/Frameworks/Electron Framework.framework/Resources/claude-icon.png",
		"Utilities/Disk Utility.app/Contents/Info.plist", "Claude notes.txt",
	} {
		write(t, root, rel, "x")
	}
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ld.Close() })
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	names := func(q string) []string {
		t.Helper()
		r, err := ld.Search(q, DefaultSearchOptions)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		out := []string{}
		for _, h := range r.Hits {
			out = append(out, h.Name)
		}
		return sorted(out)
	}
	eq(t, "claude.app", names("claude.app"), "Claude.app")
	eq(t, "claude", names("claude"), "Claude notes.txt", "Claude.app")
	eq(t, "Info.plist", names("Info.plist"))
	eq(t, "kind:app", names("kind:app"), "Claude.app", "Disk Utility.app")
	eq(t, "apps", names("apps"), "Claude.app", "Disk Utility.app")

	// A file that appears inside an app later stays out of the index.
	write(t, root, "Claude.app/Contents/Resources/new.txt", "x")
	if _, err := ld.ApplyChanges([]Change{{Path: filepath.Join(root, "Claude.app/Contents/Resources"), Recursive: true}}); err != nil {
		t.Fatal(err)
	}
	eq(t, "new.txt", names("new.txt"))
}

// An index made before apps were opaque loses their contents on refresh.
func TestAppContentsLeaveAnOlderIndex(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "Documents")
	write(t, root, "Tool.app/Contents/Info.plist", "x")
	write(t, root, "notes.txt", "x")
	db := filepath.Join(dir, "index.db")
	ld, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	saved := scan.DefaultOpaqueExts
	scan.DefaultOpaqueExts = nil // as before 0.3
	defer func() { scan.DefaultOpaqueExts = saved }()
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	if r, _ := ld.Search("Info.plist", DefaultSearchOptions); r.Total != 1 {
		t.Fatalf("setup: %d", r.Total)
	}
	scan.DefaultOpaqueExts = saved
	if _, err := ld.Index(root); err != nil { // a refresh
		t.Fatal(err)
	}
	if r, _ := ld.Search("Info.plist", DefaultSearchOptions); r.Total != 0 {
		t.Errorf("Info.plist inside an app is still indexed: %d", r.Total)
	}
	if r, _ := ld.Search("tool.app", DefaultSearchOptions); r.Total != 1 {
		t.Errorf("tool.app: %d", r.Total)
	}
	ld.Close()
}
