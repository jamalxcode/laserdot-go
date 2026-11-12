package core

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// A first index leaves tags for a background pass; once it is done, tag:
// finds the tagged file, and only it (Linux: user.xdg.tags).
func TestTagsArriveAfterTheFirstIndex(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(dir, "home")
	write(t, root, "plain.txt", "x")
	write(t, root, "deep/inside/tagged.txt", "x")
	if err := unix.Lsetxattr(filepath.Join(root, "deep/inside/tagged.txt"), "user.xdg.tags", []byte("Red,Work"), 0); err != nil {
		t.Skipf("no user xattrs here: %v", err)
	}
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	ld.WaitBackground()
	for _, q := range []string{"tag:red", "tag:work"} {
		r, err := ld.Search(q, SearchOptions{Page: pageAll()})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Hits) != 1 || r.Hits[0].Name != "tagged.txt" {
			t.Errorf("%s: %+v", q, r.Hits)
		}
	}
	// Stored too: a reopened index has the tags.
	ld.Close()
	again, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	again.reload()
	if r, _ := again.Search("tag:red", SearchOptions{Page: pageAll()}); len(r.Hits) != 1 {
		t.Errorf("after reopening: %+v", r.Hits)
	}
}
