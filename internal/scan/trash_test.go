package scan

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNumbered(t *testing.T) {
	for _, c := range []struct {
		name   string
		n      int
		finder bool
		want   string
	}{
		{"a b.txt", 2, false, "a b (2).txt"},
		{"archive", 3, false, "archive (3)"},
		{".hidden", 2, false, ".hidden (2)"},
		{"a b.txt", 2, true, "a b 2.txt"},
		{"archive", 3, true, "archive 3"},
	} {
		if got := numbered(c.name, c.n, c.finder); got != c.want {
			t.Errorf("numbered(%q, %d, %v) = %q, want %q", c.name, c.n, c.finder, got, c.want)
		}
	}
}

func TestTrashesIntoTheFreedesktopTrash(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS uses the Finder's Trash")
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("XDG_DATA_HOME", home)
	f := filepath.Join(home, "a b.txt")
	write(t, f, "x")
	to, err := Trash(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(f); !os.IsNotExist(err) {
		t.Error("still there")
	}
	if to != filepath.Join(home, "Trash/files/a b.txt") {
		t.Errorf("went to %s", to)
	}
	info, err := os.ReadFile(filepath.Join(home, "Trash/info/a b.txt.trashinfo"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(info)
	if !strings.HasPrefix(s, "[Trash Info]\n") || !strings.Contains(s, "Path=") || !strings.Contains(s, "a%20b.txt") || !strings.Contains(s, "DeletionDate=20") {
		t.Errorf("%s", s)
	}
	// Same name again: numbered.
	write(t, f, "y")
	if to, err := Trash(f); err != nil || to != filepath.Join(home, "Trash/files/a b (2).txt") {
		t.Errorf("%s %v", to, err)
	}
	if _, err := Trash(filepath.Join(home, "missing")); err == nil {
		t.Error("trashed a missing file")
	}
}

func TestTrashesIntoTheFinderTrash(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	f := filepath.Join(home, "a b.txt")
	write(t, f, "x")
	to, err := Trash(f)
	if err != nil || to != filepath.Join(home, ".Trash/a b.txt") {
		t.Fatalf("%s %v", to, err)
	}
	write(t, f, "y")
	if to, err := Trash(f); err != nil || to != filepath.Join(home, ".Trash/a b 2.txt") {
		t.Fatalf("%s %v", to, err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".Trash/a b.txt")); string(b) != "x" {
		t.Error("first trashed copy was replaced")
	}
}
