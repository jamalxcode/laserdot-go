//go:build darwin

package media

import (
	stdimage "image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// photoLike writes an 800×600 JPEG with broad shapes and gradients, like a
// photo: a picture big enough that HEIC compression keeps its fingerprint.
func photoLike(t *testing.T, path string) {
	t.Helper()
	img := stdimage.NewRGBA(stdimage.Rect(0, 0, 800, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			r, g, b := uint8(x*255/800), uint8(y*255/600), uint8(90)
			if dx, dy := x-250, y-300; dx*dx+dy*dy < 120*120 {
				r, g, b = 240, 220, 40
			}
			if x > 520 && x < 720 && y > 120 && y < 260 {
				r, g, b = 20, 30, 160
			}
			if y > 450 {
				r, g, b = r/3, g/3+60, b/3
			}
			img.Set(x, y, color.RGBA{r, g, b, 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
}

// A HEIC (every iPhone photo) gets a fingerprint through ImageIO, close to
// the same picture's JPEG one, so the two are found as similar.
func TestHEICGetsAFingerprintCloseToItsJPEG(t *testing.T) {
	dir := t.TempDir()
	jpg := filepath.Join(dir, "photo.jpg")
	photoLike(t, jpg)
	heic := filepath.Join(dir, "photo.heic")
	if out, err := exec.Command("/usr/bin/sips", "-s", "format", "heic", jpg, "--out", heic).CombinedOutput(); err != nil {
		t.Skipf("sips cannot write HEIC here: %v %s", err, out)
	}
	if !HashableImage("heic") || !HashableImage("dng") {
		t.Fatal("HEIC and RAW are hashable on macOS")
	}
	gj, ok := ImageGray(jpg, "jpg")
	if !ok {
		t.Fatal("the JPEG decodes")
	}
	// The pixel path itself: ImageIO and the pure-Go decoder agree on one JPEG.
	gi, ok := imageIOGray(jpg)
	if !ok {
		t.Fatal("ImageIO decodes the JPEG")
	}
	if d := Hamming(PHash(&gj), PHash(&gi)); d > 4 {
		t.Errorf("ImageIO and the Go decoder differ by %d bits on one JPEG (luma %d vs %d)", d, mean(&gj), mean(&gi))
	}
	gh, ok := ImageGray(heic, "heic")
	if !ok {
		t.Fatal("the HEIC decodes through ImageIO")
	}
	if d := Hamming(PHash(&gj), PHash(&gh)); d > 8 {
		t.Errorf("HEIC and JPEG of one picture differ by %d bits (luma %d vs %d)", d, mean(&gj), mean(&gh))
	}
	// Not a picture at all: no fingerprint, no crash.
	junk := filepath.Join(dir, "junk.heic")
	os.WriteFile(junk, []byte("not an image"), 0o644)
	if _, ok := ImageGray(junk, "heic"); ok {
		t.Error("a broken HEIC has no fingerprint")
	}
}

func mean(g *Gray) int {
	s := 0
	for _, v := range g {
		s += int(v)
	}
	return s / len(g)
}
