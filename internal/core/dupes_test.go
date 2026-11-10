package core

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"time"
)

func fx(t *testing.T, name string) []byte { return corpus(t, filepath.Join("dupes", name)) }

func withExif(jpeg []byte, orientation uint16, thumb []byte) []byte {
	le := binary.LittleEndian
	t := []byte("II*\x00\x08\x00\x00\x00")
	t = le.AppendUint16(t, 1)
	t = le.AppendUint16(t, 0x0112)
	t = le.AppendUint16(t, 3)
	t = le.AppendUint32(t, 1)
	t = le.AppendUint16(t, orientation)
	t = append(t, 0, 0)
	next := uint32(0)
	if thumb != nil {
		next = 26
	}
	t = le.AppendUint32(t, next)
	if thumb != nil {
		t = le.AppendUint16(t, 2)
		for _, e := range [][2]uint32{{0x0201, 56}, {0x0202, uint32(len(thumb))}} {
			t = le.AppendUint16(t, uint16(e[0]))
			t = le.AppendUint16(t, 4)
			t = le.AppendUint32(t, 1)
			t = le.AppendUint32(t, e[1])
		}
		t = le.AppendUint32(t, 0)
		t = append(t, thumb...)
	}
	payload := append([]byte("Exif\x00\x00"), t...)
	out := append([]byte{}, jpeg[:2]...)
	out = append(out, 0xFF, 0xE1)
	out = binary.BigEndian.AppendUint16(out, uint16(len(payload)+2))
	out = append(out, payload...)
	return append(out, jpeg[2:]...)
}

func dupeLibrary(t *testing.T) *lib {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "lib")
	h := time.Hour
	png := fx(t, "mandel.png")
	put(t, filepath.Join(root, "Photos/mandel.png"), png, 9000*time.Second)
	put(t, filepath.Join(root, "Backup/mandel copy.png"), png, h)
	put(t, filepath.Join(root, "Web/mandel-small.jpg"), fx(t, "mandel_small.jpg"), h)
	put(t, filepath.Join(root, "Web/mandel-thumb.jpg"), fx(t, "mandel_thumb.jpg"), h)
	put(t, filepath.Join(root, "Phone/turned.jpg"), withExif(fx(t, "mandel_turned.jpg"), 8, nil), h)
	put(t, filepath.Join(root, "Phone/thumbnail-only.jpg"), withExif(fx(t, "other.jpg"), 1, fx(t, "mandel_thumb.jpg")), h)
	put(t, filepath.Join(root, "Photos/other.jpg"), fx(t, "other.jpg"), h)
	a := bytes.Repeat([]byte{7}, 400_000)
	put(t, filepath.Join(root, "Data/a.bin"), a, h)
	a[100_000] = 8
	put(t, filepath.Join(root, "Data/b.bin"), a, h)
	os.Link(filepath.Join(root, "Photos/other.jpg"), filepath.Join(root, "Photos/other-link.jpg"))
	for name, to := range map[string]string{"clip.mp4": "Movies/clip.mp4", "clip_hevc.mov": "Movies/Exports/clip small.mov",
		"clip_low.m4v": "Old/clip.m4v", "other_clip.mp4": "Movies/fractal.mp4"} {
		put(t, filepath.Join(root, to), fx(t, name), h)
	}
	ld, err := Open(filepath.Join(base, "data/index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ld.Close() })
	ld.Index(root)
	return &lib{root, ld}
}

func groupNames(r *DupeReport, kind string) [][]string {
	var v [][]string
	for _, g := range r.Groups {
		if g.Kind != kind {
			continue
		}
		var n []string
		for _, m := range g.Members {
			n = append(n, filepath.Base(m.Path))
		}
		slices.Sort(n)
		v = append(v, n)
	}
	return v
}

func exactOnly() DupeOptions {
	o := DefaultDupeOptions()
	o.Images, o.Videos = false, false
	return o
}

func TestStory23FindsExactSimilarAndReencoded(t *testing.T) {
	l := dupeLibrary(t)
	r, err := l.ld.FindDuplicates(DefaultDupeOptions())
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"mandel copy.png", "mandel-small.jpg", "mandel-thumb.jpg", "mandel.png", "thumbnail-only.jpg", "turned.jpg"}}
	if got := groupNames(r, DupeSimilarImage); !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
	var g DupeGroup
	for _, x := range r.Groups {
		if x.Kind == DupeSimilarImage {
			g = x
		}
	}
	if !g.Members[0].Keep || slices.ContainsFunc(g.Members[1:], func(m DupeMember) bool { return m.Keep }) {
		t.Error("keep")
	}
	if g.Members[0].Path != filepath.Join(l.root, "Photos/mandel.png") || g.Members[1].Path != filepath.Join(l.root, "Backup/mandel copy.png") ||
		*g.Members[len(g.Members)-1].Width != 160 {
		t.Errorf("%+v", g.Members)
	}
	for _, x := range r.Groups {
		for _, m := range x.Members {
			n := filepath.Base(m.Path)
			if filepath.Ext(n) == ".bin" || n == "other.jpg" || n == "other-link.jpg" {
				t.Errorf("not a duplicate: %s", n)
			}
		}
	}
	if r.VideoDecoder != nil {
		if got := groupNames(r, DupeReencodedVideo); !reflect.DeepEqual(got, [][]string{{"clip small.mov", "clip.m4v", "clip.mp4"}}) {
			t.Errorf("%q", got)
		}
		for _, x := range r.Groups {
			if x.Kind == DupeReencodedVideo && (*x.Members[0].Width != 320 || *x.Members[len(x.Members)-1].Width != 160) {
				t.Errorf("%+v", x.Members)
			}
		}
	} else {
		t.Log("no video decoder: re-encode detection not tested here")
	}
	var sum uint64
	for _, x := range r.Groups {
		sum += x.Reclaimable
	}
	if sum != r.Reclaimable {
		t.Error("reclaimable")
	}
}

func TestExactOnlyAndScoped(t *testing.T) {
	l := dupeLibrary(t)
	r, _ := l.ld.FindDuplicates(exactOnly())
	if got := groupNames(r, DupeExact); !reflect.DeepEqual(got, [][]string{{"mandel copy.png", "mandel.png"}}) || len(r.Groups) != 1 {
		t.Errorf("%q", got)
	}
	if r.Reclaimable != uint64(len(fx(t, "mandel.png"))) {
		t.Error(r.Reclaimable)
	}
	o := DefaultDupeOptions()
	o.Scope = "in:Photos"
	if r, _ := l.ld.FindDuplicates(o); len(r.Groups) != 0 {
		t.Errorf("%+v", r.Groups)
	}
	o.Scope = "ext:png"
	if r, _ := l.ld.FindDuplicates(o); len(r.Groups) != 1 || r.Groups[0].Kind != DupeExact {
		t.Errorf("%+v", r.Groups)
	}
	o.Scope = "res:4l"
	if _, err := l.ld.FindDuplicates(o); err == nil {
		t.Error("bad scope")
	}
}

func TestSecondScanReadsNothingAndChangesAreNoticed(t *testing.T) {
	l := dupeLibrary(t)
	first, _ := l.ld.FindDuplicates(DefaultDupeOptions())
	again, _ := l.ld.FindDuplicates(DefaultDupeOptions())
	if first.Read == 0 || again.Read != 0 || len(again.Groups) != len(first.Groups) {
		t.Errorf("%d %d", first.Read, again.Read)
	}
	put(t, filepath.Join(l.root, "Backup/mandel copy.png"), append(fx(t, "mandel.png"), "trailing bytes"...), time.Hour)
	l.ld.Refresh()
	r, _ := l.ld.FindDuplicates(exactOnly())
	if len(r.Groups) != 0 || r.Read != 0 {
		t.Errorf("%+v", r)
	}
}

func TestStory25DryRunThenMoveToAFolder(t *testing.T) {
	l := dupeLibrary(t)
	report, _ := l.ld.FindDuplicates(exactOnly())
	keep, cp := filepath.Join(l.root, "Photos/mandel.png"), filepath.Join(l.root, "Backup/mandel copy.png")
	bin := filepath.Join(filepath.Dir(l.root), "removed")
	os.MkdirAll(bin, 0o755)
	req := report.Recommended(CleanupDest{To: "folder", Folder: bin})
	plan := l.ld.PlanCleanup(req)
	if len(plan.Moves) != 1 || plan.Moves[0].From != cp || *plan.Moves[0].To != filepath.Join(bin, "mandel copy.png") ||
		plan.Bytes != report.Reclaimable || len(plan.Skipped) != 0 {
		t.Fatalf("%+v", plan)
	}
	done, err := l.ld.ApplyCleanup(req)
	if err != nil || len(done.Moved) != 1 || len(done.Failed) != 0 {
		t.Fatalf("%+v %v", done, err)
	}
	if _, err := os.Stat(cp); err == nil {
		t.Error("copy still there")
	}
	if _, err := os.Stat(filepath.Join(bin, "mandel copy.png")); err != nil {
		t.Error(err)
	}
	expect(t, lpaths(l.ld, "mandel ext:png"), keep)
	again, _ := l.ld.ApplyCleanup(req)
	if len(again.Moved) != 0 || len(again.Skipped) != 1 {
		t.Errorf("%+v", again)
	}
}

func TestStory25ChangedOrMissingFilesAreLeftAlone(t *testing.T) {
	l := dupeLibrary(t)
	report, _ := l.ld.FindDuplicates(exactOnly())
	bin := filepath.Join(filepath.Dir(l.root), "removed")
	os.MkdirAll(bin, 0o755)
	req := report.Recommended(CleanupDest{To: "folder", Folder: bin})
	put(t, filepath.Join(l.root, "Photos/mandel.png"), []byte("edited"), time.Minute)
	plan := l.ld.PlanCleanup(req)
	if len(plan.Moves) != 0 || !bytes.Contains([]byte(plan.Skipped[0]), []byte("changed since the scan")) {
		t.Errorf("%+v", plan)
	}
	if d, _ := l.ld.ApplyCleanup(req); len(d.Moved) != 0 {
		t.Error("moved")
	}
	none := CleanupRequest{Groups: []CleanupGroup{{Remove: req.Groups[0].Remove}}, Dest: req.Dest}
	if len(l.ld.PlanCleanup(none).Moves) != 0 {
		t.Error("nothing to keep")
	}
	bad := req
	bad.Dest = CleanupDest{To: "folder", Folder: filepath.Join(l.root, "nope")}
	if len(l.ld.PlanCleanup(bad).Moves) != 0 {
		t.Error("bad dest")
	}
}

func TestStory25Trash(t *testing.T) {
	l := dupeLibrary(t)
	trashHome := filepath.Join(filepath.Dir(l.root), "xdg")
	t.Setenv("XDG_DATA_HOME", trashHome)
	if os.Getenv("HOME") != "" && runtimeIsDarwin() {
		t.Setenv("HOME", filepath.Dir(l.root)) // keep the test's Trash in its temp folder
		os.MkdirAll(filepath.Join(filepath.Dir(l.root), ".Trash"), 0o700)
	}
	report, _ := l.ld.FindDuplicates(exactOnly())
	done, err := l.ld.ApplyCleanup(report.Recommended(CleanupDest{To: "trash"}))
	if err != nil || len(done.Moved) != 1 {
		t.Fatalf("%+v %v", done, err)
	}
	if _, err := os.Stat(filepath.Join(l.root, "Backup/mandel copy.png")); err == nil {
		t.Error("still there")
	}
	if done.Moved[0].To == nil {
		t.Fatal("no destination")
	}
	if _, err := os.Stat(*done.Moved[0].To); err != nil {
		t.Error(err)
	}
}

func TestCopiesAcrossADrive(t *testing.T) {
	l := dupeLibrary(t)
	vols := filepath.Join(filepath.Dir(l.root), "Volumes")
	drive := filepath.Join(vols, "Archive")
	put(t, filepath.Join(drive, "Pictures/mandel.png"), fx(t, "mandel.png"), 99_000*time.Second)
	l.ld.SetVolumeConfig(VolumeConfig{Dir: vols})
	if _, err := l.ld.CreateDriveIndex(drive, DriveOptions{}); err != nil {
		t.Fatal(err)
	}
	r, _ := l.ld.FindDuplicates(exactOnly())
	if len(r.Groups) != 1 || len(r.Groups[0].Members) != 3 {
		t.Fatalf("%+v", r.Groups)
	}
	g := r.Groups[0]
	if g.Members[0].Path != filepath.Join(l.root, "Photos/mandel.png") || g.Members[0].Volume != nil {
		t.Errorf("this Mac's original is kept: %+v", g.Members[0])
	}
	i := slices.IndexFunc(g.Members, func(m DupeMember) bool { return m.Volume != nil })
	if i < 0 || *g.Members[i].Volume != "Archive" || g.Members[i].Path != filepath.Join(drive, "Pictures/mandel.png") {
		t.Errorf("%+v", g.Members)
	}
	if again, _ := l.ld.FindDuplicates(exactOnly()); again.Read != 0 {
		t.Error("cache")
	}
}

func TestNearPairsFindsEveryPairWithinTheDistance(t *testing.T) {
	x := uint64(0x9E3779B97F4A7C15)
	var hashes []hashedImage
	for i := 0; i < 400; i++ {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		h := x
		if i%4 != 0 {
			h = hashes[len(hashes)-1].hash ^ (x & 0x0101010101010101 & (x >> 9))
		}
		hashes = append(hashes, hashedImage{i, h})
	}
	for _, m := range []int{0, 3, 8, 11} {
		var brute [][2]int
		for a := range hashes {
			for b := a + 1; b < len(hashes); b++ {
				if popcount(hashes[a].hash^hashes[b].hash) <= m {
					brute = append(brute, [2]int{a, b})
				}
			}
		}
		if got := nearPairs(hashes, m); !reflect.DeepEqual(got, brute) {
			t.Errorf("max %d: %d pairs, want %d", m, len(got), len(brute))
		}
	}
}

func popcount(x uint64) int {
	n := 0
	for ; x != 0; x &= x - 1 {
		n++
	}
	return n
}

func TestFlatFramesOnlyMatchFlatFrames(t *testing.T) {
	if !framesMatch([3]uint64{1, 2, 3}, 1, [3]uint64{9, 2, 3}, 1, 0) || framesMatch([3]uint64{1, 2, 3}, 1, [3]uint64{1, 2, 3}, 0, 12) ||
		framesMatch([3]uint64{}, 7, [3]uint64{}, 7, 12) {
		t.Error("frames")
	}
}

func runtimeIsDarwin() bool { return runtime.GOOS == "darwin" }
