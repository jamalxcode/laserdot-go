package index

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jamalxcode/laserdot-go/internal/scan"
)

func walk(t *testing.T, root string) []scan.Entry {
	t.Helper()
	es, _, err := scan.Walk(root, scan.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func TestDbRoundTrip(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "files")
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	os.WriteFile(filepath.Join(root, "sub", "invoice march.pdf"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o644)
	dbPath := filepath.Join(dir, "idx", "index.db")
	db, rep, err := Open(dbPath)
	if err != nil || rep.Quarantined != "" {
		t.Fatal(err, rep)
	}
	if err := db.ReplaceRoot(root, walk(t, root)); err != nil {
		t.Fatal(err)
	}
	// Re-indexing replaces rather than duplicates.
	if err := db.ReplaceRoot(root, walk(t, root)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, _, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, err := db.Load()
	if err != nil {
		t.Fatal(err)
	}
	s := Build(raw)
	if s.LiveCount() != 3 || !reflect.DeepEqual(s.Roots(), []string{root}) {
		t.Fatalf("%d %v", s.LiveCount(), s.Roots())
	}
	id, ok := s.Resolve(filepath.Join(root, "notes.txt"))
	if !ok || s.Size(id) != 5 {
		t.Error("notes.txt")
	}
	roots, _ := db.Roots()
	if len(roots) != 1 || !roots[0].Complete {
		t.Errorf("%+v", roots)
	}
	if ok, _ := db.RemoveRoot(root); !ok {
		t.Error("remove")
	}
	if raw, _ := db.Load(); len(raw) != 0 {
		t.Errorf("%d rows left", len(raw))
	}
}

func TestCorruptIndexIsQuarantined(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "files")
	os.MkdirAll(root, 0o755)
	dbPath := filepath.Join(dir, "index.db")
	db, _, _ := Open(dbPath)
	db.ReplaceRoot(root, walk(t, root))
	db.Close()
	os.WriteFile(dbPath, []byte("this is not a database, just garbage bytes......"), 0o600)
	os.Remove(dbPath + "-wal")
	db, rep, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if rep.Quarantined == "" || !reflect.DeepEqual(rep.RootsToReindex, []string{root}) {
		t.Errorf("%+v", rep)
	}
	if _, err := os.Stat(rep.Quarantined); err != nil {
		t.Error(err)
	}
}
