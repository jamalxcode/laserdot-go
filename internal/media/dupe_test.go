package media

// Fingerprints on the duplicate corpus (testdata/dupes, spec §4.1).

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fx(name string) string { return filepath.Join("testdata", "dupes", name) }

func img(t *testing.T, name string) uint64 {
	t.Helper()
	ext := name[strings.LastIndexByte(name, '.')+1:]
	g, ok := ImageGray(fx(name), ext)
	if !ok {
		t.Fatalf("%s did not decode", name)
	}
	if IsFlat(&g) {
		t.Fatalf("%s is flat", name)
	}
	return PHash(&g)
}

func TestSimilarImagesAreCloseAndDifferentOnesFar(t *testing.T) {
	png := img(t, "mandel.png")
	for _, c := range []string{"mandel_small.jpg", "mandel_thumb.jpg"} {
		if d := Hamming(png, img(t, c)); d > 7 {
			t.Errorf("%s: distance %d", c, d)
		}
	}
	if d := Hamming(png, img(t, "other.jpg")); d < 20 {
		t.Errorf("other.jpg: distance %d", d)
	}
	// Turned 90° clockwise: far as stored, close once EXIF orientation 8
	// (90° counter-clockwise) is applied.
	g, ok := ImageGray(fx("mandel_turned.jpg"), "jpg")
	if !ok {
		t.Fatal("mandel_turned.jpg did not decode")
	}
	if d := Hamming(png, PHash(&g)); d < 20 {
		t.Errorf("turned: distance %d", d)
	}
	back := Orient(&g, 8)
	if d := Hamming(png, PHash(&back)); d > 7 {
		t.Errorf("turned back: distance %d", d)
	}
}

func size(t *testing.T, p string) uint64 {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return uint64(st.Size())
}

func must(t *testing.T) func([32]byte, error) [32]byte {
	return func(v [32]byte, err error) [32]byte {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func TestExactHashes(t *testing.T) {
	big := fx("mandel.png")
	n := size(t, big)
	if n <= 3*Window || QuickIsFull(n) {
		t.Fatalf("size %d", n)
	}
	q := must(t)(QuickHash(big, n))
	if q != must(t)(QuickHash(big, n)) {
		t.Error("quick hash not stable")
	}
	if q == must(t)(FullHash(big)) {
		t.Error("quick hash equals full hash for a big file")
	}
	small := fx("mandel_small.jpg")
	s := size(t, small)
	if !QuickIsFull(s) {
		t.Error("small file not fully covered")
	}
	if must(t)(QuickHash(small, s)) == q {
		t.Error("different files, same quick hash")
	}
}

// TestReencodedVideoFramesMatch: re-encodes (other codec, container, size
// and bitrate) match frame by frame; a different clip does not. Needs
// ffmpeg (skipped when there is none).
func TestReencodedVideoFramesMatch(t *testing.T) {
	if VideoBackend() == "" {
		t.Skip("no video decoder")
	}
	frames := func(n string) []uint64 {
		gs, err := VideoFrames(fx(n), 4000)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if len(gs) != 3 {
			t.Fatalf("%s: %d frames", n, len(gs))
		}
		var out []uint64
		for i := range gs {
			out = append(out, PHash(&gs[i]))
		}
		return out
	}
	orig := frames("clip.mp4")
	for _, c := range []string{"clip_hevc.mov", "clip_low.m4v"} {
		for i, h := range frames(c) {
			if d := Hamming(orig[i], h); d > 12 {
				t.Errorf("%s frame %d: distance %d", c, i, d)
			}
		}
	}
	far := 0
	for i, h := range frames("other_clip.mp4") {
		if Hamming(orig[i], h) > 12 {
			far++
		}
	}
	if far < 2 {
		t.Error("other clip should differ")
	}
}

func pattern(f func(x, y int) byte) Gray {
	var g Gray
	for y := range Side {
		for x := range Side {
			g[y*Side+x] = f(x, y)
		}
	}
	return g
}

func TestPHashToleratesSmallChangesAndSeparatesContent(t *testing.T) {
	a := pattern(func(x, y int) byte { return byte((x*7 + y*3) % 32 * 8) })
	brighter := pattern(func(x, y int) byte { return byte(min((x*7+y*3)%32*8+12, 255)) })
	other := pattern(func(x, y int) byte {
		if (x/8+y/8)%2 == 0 {
			return 30
		}
		return 220
	})
	if d := Hamming(PHash(&a), PHash(&brighter)); d > 4 {
		t.Errorf("brighter: %d", d)
	}
	if d := Hamming(PHash(&a), PHash(&other)); d <= 16 {
		t.Errorf("other: %d", d)
	}
	if IsFlat(&a) {
		t.Error("pattern is flat")
	}
	var flat Gray
	for i := range flat {
		flat[i] = 128
	}
	if !IsFlat(&flat) {
		t.Error("uniform is not flat")
	}
}

func TestOrientationRoundTrips(t *testing.T) {
	g := pattern(func(x, y int) byte { return byte(x*8 + y) })
	o6 := Orient(&g, 6)
	o3 := Orient(&g, 3)
	// 6 (90° clockwise) then 8 (90° counter-clockwise) is the identity.
	if Orient(&o6, 8) != g || Orient(&o3, 3) != g || Orient(&g, 1) != g {
		t.Error("orientation does not round-trip")
	}
}

func TestDownscaleAverages(t *testing.T) {
	luma := make([]byte, 64*48)
	for i := range luma {
		luma[i] = 100
	}
	g, ok := Downscale(luma, 64, 48)
	var want Gray
	for i := range want {
		want[i] = 100
	}
	if !ok || g != want {
		t.Error("downscale")
	}
	if _, ok := Downscale(luma, 0, 48); ok {
		t.Error("zero width accepted")
	}
}

// withExif inserts an APP1 EXIF segment (orientation, and an IFD1 JPEG
// thumbnail when thumb is set) after the SOI of a JPEG.
func withExif(jpg []byte, orientation uint16, thumb []byte) []byte {
	be := binary.BigEndian
	t := []byte("MM\x00*\x00\x00\x00\x08")
	t = be.AppendUint16(t, 1)
	t = be.AppendUint16(t, 0x112)
	t = be.AppendUint16(t, 3)
	t = be.AppendUint32(t, 1)
	t = be.AppendUint16(t, orientation)
	t = be.AppendUint16(t, 0)
	next := uint32(0)
	if thumb != nil {
		next = 26
	}
	t = be.AppendUint32(t, next)
	if thumb != nil {
		t = be.AppendUint16(t, 2)
		for _, e := range [][2]uint32{{0x201, 56}, {0x202, uint32(len(thumb))}} {
			t = be.AppendUint16(t, uint16(e[0]))
			t = be.AppendUint16(t, 4)
			t = be.AppendUint32(t, 1)
			t = be.AppendUint32(t, e[1])
		}
		t = be.AppendUint32(t, 0)
		t = append(t, thumb...)
	}
	out := []byte{0xFF, 0xD8, 0xFF, 0xE1}
	out = be.AppendUint16(out, uint16(len(t)+8))
	out = append(out, "Exif\x00\x00"...)
	out = append(out, t...)
	return append(out, jpg[2:]...)
}

func TestExifOrientationAndThumbnailAreUsed(t *testing.T) {
	dir := t.TempDir()
	png := img(t, "mandel.png")
	turned, err := os.ReadFile(fx("mandel_turned.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "turned.jpg")
	if err := os.WriteFile(p, withExif(turned, 8, nil), 0o644); err != nil {
		t.Fatal(err)
	}
	g, ok := ImageGray(p, "jpg")
	if !ok {
		t.Fatal("did not decode")
	}
	if d := Hamming(png, PHash(&g)); d > 7 {
		t.Errorf("orientation 8 applied: distance %d", d)
	}

	// A thumbnail stands in for the image: here, a different picture.
	small, err1 := os.ReadFile(fx("mandel_small.jpg"))
	other, err2 := os.ReadFile(fx("other.jpg"))
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if len(other) > 60000 {
		t.Skip("thumbnail too big for one APP1 segment")
	}
	p = filepath.Join(dir, "thumb.jpg")
	if err := os.WriteFile(p, withExif(small, 1, other), 0o644); err != nil {
		t.Fatal(err)
	}
	g, ok = ImageGray(p, "jpg")
	if !ok {
		t.Fatal("did not decode")
	}
	if d := Hamming(img(t, "other.jpg"), PHash(&g)); d > 7 {
		t.Errorf("thumbnail not used: distance %d", d)
	}
}
