package scan

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rel(t *testing.T, root string, entries []Entry) []string {
	t.Helper()
	root, _ = filepath.EvalSymlinks(root)
	var out []string
	for _, e := range entries {
		r, _ := filepath.Rel(root, e.Path)
		out = append(out, r)
	}
	return out
}

func has(list []string, s string) bool { return contains(list, s) }

func TestWalksWithIgnoreRules(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a/b/file.txt"), "hello")
	write(t, filepath.Join(dir, "node_modules/pkg/index.js"), "x")
	write(t, filepath.Join(dir, ".git/objects/abc"), "x")
	write(t, filepath.Join(dir, "movie.mkv.part"), "x")
	entries, errs, err := Walk(dir, DefaultOptions())
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	names := rel(t, dir, entries)
	if !has(names, "a/b/file.txt") || !has(names, ".git") {
		t.Errorf("%v", names)
	}
	for _, n := range names {
		if strings.HasPrefix(n, "node_modules") || strings.HasPrefix(n, ".git/") || strings.HasSuffix(n, ".part") {
			t.Errorf("should be skipped: %s", n)
		}
	}
	for i, e := range entries {
		if e.Parent >= 0 && (e.Parent >= i || filepath.Dir(e.Path) != entries[e.Parent].Path) {
			t.Errorf("%s: bad parent %d", e.Path, e.Parent)
		}
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Path, "file.txt") && e.Size != 5 {
			t.Errorf("size %d", e.Size)
		}
	}
}

func TestSymlinksAreRecordedNotFollowed(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "real/x.txt"), "x")
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "link")); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(dir, filepath.Join(dir, "real/loop")); err != nil {
		t.Skip(err)
	}
	entries, _, err := Walk(dir, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	names := rel(t, dir, entries)
	if !has(names, "link") || has(names, "link/x.txt") || has(names, "real/loop/real") {
		t.Errorf("%v", names)
	}
}

func TestHardLinksAreListedOnce(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "b/one.txt"), "x")
	if err := os.Link(filepath.Join(dir, "b/one.txt"), filepath.Join(dir, "a.txt")); err != nil {
		t.Skip(err)
	}
	entries, _, _ := Walk(dir, DefaultOptions())
	names := rel(t, dir, entries)
	if !has(names, "a.txt") || has(names, "b/one.txt") {
		t.Errorf("want only the smallest path, got %v", names)
	}
}

// /dev/pts is a separate mount inside /dev on Linux: a walk of /dev lists it
// but never enters it (§17.6).
func TestWalksStopAtOtherVolumes(t *testing.T) {
	if runtime.GOOS != "linux" || !IsMountPoint("/dev/pts") {
		t.Skip("/dev/pts is not a separate mount here")
	}
	entries, _, err := Walk("/dev", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	listed, entered := false, false
	for _, e := range entries {
		listed = listed || e.Path == "/dev/pts"
		entered = entered || strings.HasPrefix(e.Path, "/dev/pts/")
	}
	if !listed || entered {
		t.Errorf("listed %v, entered %v", listed, entered)
	}
}

// Indexing shows how many files were found while it walks: the count ends
// at every entry under the folder.
func TestWalkCountsWhatItFinds(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"a.txt", "sub/b.txt", "sub/deeper/c.txt"} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var found atomic.Uint64
	entries, _, err := Walk(dir, Options{Found: &found})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := found.Load(), uint64(len(entries)-1); got != want || got != 5 {
		t.Fatalf("found %d, want %d (5: three files, two folders)", got, want)
	}
}
