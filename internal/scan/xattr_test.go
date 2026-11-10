package scan

import (
	"encoding/binary"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

// finderPlist is a real Finder tag plist: ["Red\n6", "Projects"].
func finderPlist() []byte {
	b := []byte("bplist00")
	oArr := len(b)
	b = append(b, 0xA2, 1, 2)
	oRed := len(b)
	b = append(b, 0x55)
	b = append(b, "Red\n6"...)
	oProj := len(b)
	b = append(b, 0x58)
	b = append(b, "Projects"...)
	table := len(b)
	b = append(b, byte(oArr), byte(oRed), byte(oProj))
	var trailer [32]byte
	trailer[6], trailer[7] = 1, 1
	binary.BigEndian.PutUint64(trailer[8:], 3)
	binary.BigEndian.PutUint64(trailer[16:], 0)
	binary.BigEndian.PutUint64(trailer[24:], uint64(table))
	return append(b, trailer[:]...)
}

func TestParsesFinderTags(t *testing.T) {
	got, ok := bplistStrings(finderPlist())
	if !ok || !reflect.DeepEqual(got, []string{"Red\n6", "Projects"}) {
		t.Fatalf("%q %v", got, ok)
	}
}

func TestParsesUTF16AndLongStrings(t *testing.T) {
	// ["Größe" as UTF-16, a 20-byte ASCII string with an int length]
	b := []byte("bplist00")
	oArr := len(b)
	b = append(b, 0xA2, 1, 2)
	oU := len(b)
	b = append(b, 0x65)
	for _, r := range "Größe" {
		b = binary.BigEndian.AppendUint16(b, uint16(r))
	}
	oL := len(b)
	b = append(b, 0x5F, 0x10, 20)
	b = append(b, "abcdefghijklmnopqrst"...)
	table := len(b)
	b = append(b, byte(oArr), byte(oU), byte(oL))
	var tr [32]byte
	tr[6], tr[7] = 1, 1
	binary.BigEndian.PutUint64(tr[8:], 3)
	binary.BigEndian.PutUint64(tr[24:], uint64(table))
	b = append(b, tr[:]...)
	got, ok := bplistStrings(b)
	if !ok || !reflect.DeepEqual(got, []string{"Größe", "abcdefghijklmnopqrst"}) {
		t.Fatalf("%q %v", got, ok)
	}
}

func TestGarbagePlistsDoNotPanic(t *testing.T) {
	good := finderPlist()
	for i := range good {
		b := append([]byte(nil), good...)
		b[i] ^= 0xFF
		bplistStrings(b)
		bplistStrings(good[:i])
	}
	if _, ok := bplistStrings([]byte("not a plist at all, but long enough to try")); ok {
		t.Error("accepted garbage")
	}
}

func FuzzBplistStrings(f *testing.F) {
	f.Add(finderPlist())
	f.Add([]byte("bplist00"))
	f.Fuzz(func(t *testing.T, b []byte) {
		out, _ := bplistStrings(b)
		if len(out) > 4096 {
			t.Fatalf("%d strings", len(out))
		}
	})
}

// Linux: user.xdg.tags is read as a comma list, and Walk fills Entry.Tags.
func TestReadsXdgTags(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("user.xdg.tags is the Linux fallback")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "tagged.txt")
	write(t, f, "x")
	write(t, filepath.Join(dir, "plain.txt"), "x")
	if err := unix.Lsetxattr(f, xdgTagsAttr, []byte("Red, Projects,,"), 0); err != nil {
		t.Skipf("no user xattrs here: %v", err)
	}
	if got := ReadTags(f); !reflect.DeepEqual(got, []string{"Red", "Projects"}) {
		t.Fatalf("%q", got)
	}
	entries, _, err := Walk(dir, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		want := []string(nil)
		if filepath.Base(e.Path) == "tagged.txt" {
			want = []string{"Red", "Projects"}
		}
		if !reflect.DeepEqual(e.Tags, want) {
			t.Errorf("%s: tags %q", e.Path, e.Tags)
		}
	}
}

// macOS: the Finder tag plist is read, colours stripped.
func TestReadsFinderTags(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Finder tags are macOS only")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "tagged.txt")
	write(t, f, "x")
	if err := unix.Setxattr(f, finderTagsAttr, finderPlist(), 0); err != nil {
		t.Skipf("cannot set xattr: %v", err)
	}
	if got := ReadTags(f); !reflect.DeepEqual(got, []string{"Red", "Projects"}) {
		t.Fatalf("%q", got)
	}
	entries, _, _ := Walk(dir, DefaultOptions())
	if len(entries) != 2 || !reflect.DeepEqual(entries[1].Tags, []string{"Red", "Projects"}) {
		t.Fatalf("%+v", entries)
	}
}

func TestLocalFoldersAreNotNetworkVolumes(t *testing.T) {
	if IsNetworkVolume(t.TempDir()) {
		t.Error("temp dir reported as a network volume")
	}
	if IsNetworkVolume(filepath.Join(t.TempDir(), "missing")) {
		t.Error("missing path reported as a network volume")
	}
}
