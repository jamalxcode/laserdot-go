package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jamalxcode/laserdot-go/internal/index"
)

// A full index builds the in-memory index from what it just wrote; it must
// be exactly what reading the database back gives, with other folders
// already indexed and a folder inside the new one replaced.
func TestIndexKeepsTheSameIndexAsReopening(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	write(t, a, "invoice march.pdf", "x")
	write(t, a, "sub/photo.jpg", "not really a jpeg")
	write(t, b, "notes.txt", "y")
	write(t, b, "inner/clip.mp4", "z")
	if err := os.Symlink("notes.txt", filepath.Join(b, "link")); err != nil {
		t.Fatal(err)
	}
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{a, filepath.Join(b, "inner"), b} {
		if _, err := ld.Index(root); err != nil {
			t.Fatal(err)
		}
	}
	queries := []string{"*", "kind:video", "in:" + b, "march", "type:folder"}
	live := map[string]string{}
	for _, q := range queries {
		live[q] = resultsJSON(t, ld, q)
	}
	ld.Close()
	again, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	again.reload() // from the database, not the snapshot
	for _, q := range queries {
		if got := resultsJSON(t, again, q); got != live[q] {
			t.Errorf("%s:\nlive     %s\nreopened %s", q, live[q], got)
		}
	}
}

func resultsJSON(t *testing.T, ld *Laserdot, q string) string {
	t.Helper()
	r, err := ld.Search(q, SearchOptions{Page: pageAll()})
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	r.ElapsedUS = 0
	data, _ := json.Marshal(r)
	return string(data)
}

func pageAll() index.Page {
	p := index.DefaultPage
	p.Limit = 1000
	return p
}

// A folder that cannot be read is listed, not silently missing.
func TestUnreadableFoldersAreListed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(dir, "home")
	write(t, root, "ok.txt", "x")
	write(t, root, "secret/hidden.txt", "y")
	locked := filepath.Join(root, "secret")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	r, err := ld.Unreadable()
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != 1 || len(r.Items) != 1 || r.Items[0].Path != locked || r.Items[0].Reason != "permission" {
		t.Fatalf("unreadable: %+v", r)
	}
	// Readable again and indexed again: the list empties.
	os.Chmod(locked, 0o755)
	if _, err := ld.Forget(root); err != nil {
		t.Fatal(err)
	}
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	if r, _ := ld.Unreadable(); r.Total != 0 || len(r.Items) != 0 {
		t.Fatalf("after fixing: %+v", r)
	}
}
