//go:build darwin

package scan

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Reading a folder in bulk gives exactly the entries that reading names and
// lstat-ing each one gives: files, folders, symlinks, hard links, odd names,
// sizes, dates, inodes, the ignore rules, tags.
func TestBulkMatchesStat(t *testing.T) {
	if !bulkAvailable() {
		t.Fatal("getattrlistbulk is not available")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	write(t, filepath.Join(dir, "a.txt"), "hello")
	write(t, filepath.Join(dir, "Ünïcødé ✓ name.jpg"), "1234567")
	write(t, filepath.Join(dir, "sub", "inner.pdf"), "x")
	write(t, filepath.Join(dir, "node_modules", "pkg.js"), "x") // ignored folder
	write(t, filepath.Join(dir, "big.download"), "x")           // half-downloaded
	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, "a.txt"), filepath.Join(dir, "hard.txt")); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)
	os.Chtimes(filepath.Join(dir, "sub", "inner.pdf"), old, old)
	tagged := filepath.Join(dir, "tagged.txt")
	write(t, tagged, "t")
	if err := unix.Setxattr(tagged, finderTagsAttr, finderPlist(), 0); err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	for _, deferTags := range []bool{false, true} {
		opts.DeferTags = deferTags
		bulk, berrs, ok := readDirBulk(dir, 3, opts)
		if !ok {
			t.Fatal("bulk read refused")
		}
		stat, serrs := readDirStat(dir, 3, opts)
		if len(berrs) != 0 || len(serrs) != 0 {
			t.Fatalf("errors: %v %v", berrs, serrs)
		}
		if len(bulk) != len(stat) {
			t.Fatalf("bulk %d entries, stat %d:\n%+v\n%+v", len(bulk), len(stat), bulk, stat)
		}
		for i := range stat {
			b, s := bulk[i], stat[i]
			if s.IsDir {
				s.Nlink, b.Nlink = 0, 0 // a folder's link count is not used
			}
			if !reflect.DeepEqual(b, s) {
				t.Errorf("tags deferred=%v\nbulk %+v\nstat %+v", deferTags, b, s)
			}
		}
	}
	// A whole walk (in bulk) finds the same: root, a/hard (hard links are
	// listed once), Ünïcødé, sub, inner, link, tagged.
	w, _, err := Walk(dir, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(w) != 7 {
		t.Errorf("walk found %d entries: %+v", len(w), w)
	}
}

// A folder that cannot be opened is reported, as reading names reports it.
func TestBulkReportsUnreadableFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	os.Mkdir(locked, 0)
	defer os.Chmod(locked, 0o755)
	_, errs, ok := readDirBulk(locked, 0, DefaultOptions())
	if !ok || len(errs) != 1 || errs[0].Path != locked {
		t.Fatalf("ok=%v errs=%v", ok, errs)
	}
}
