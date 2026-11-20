//go:build darwin

package core

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// An iPhone photo (HEIC) and a JPEG copy of the same picture are found as
// similar pictures (HEIC had no fingerprint before 0.9.27).
func TestHEICAndItsJPEGAreSimilarPictures(t *testing.T) {
	ld, root := newCore(t)
	writeAged(t, filepath.Join(root, "Photos/beach.jpg"), photoJPEG(t))
	heic := filepath.Join(root, "Photos/IMG_0001.HEIC")
	if out, err := exec.Command("/usr/bin/sips", "-s", "format", "heic", filepath.Join(root, "Photos/beach.jpg"), "--out", heic).CombinedOutput(); err != nil {
		t.Skipf("sips cannot write HEIC here: %v %s", err, out)
	}
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	opts := DefaultDupeOptions()
	opts.Exact, opts.Videos = false, false
	rep, err := ld.FindDuplicates(opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range rep.Groups {
		var names []string
		for _, f := range g.Members {
			names = append(names, filepath.Base(f.Path))
		}
		if len(names) == 2 {
			return
		}
	}
	t.Fatalf("the HEIC and its JPEG are not grouped: %+v", rep.Groups)
}

// photoJPEG: an 800×600 picture with broad shapes, big enough that HEIC
// compression keeps its fingerprint (a tiny test image does not).
func photoJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			r, g, b := uint8(x*255/800), uint8(y*255/600), uint8(90)
			if dx, dy := x-250, y-300; dx*dx+dy*dy < 120*120 {
				r, g, b = 240, 220, 40
			}
			if x > 520 && x < 720 && y > 120 && y < 260 {
				r, g, b = 20, 30, 160
			}
			img.Set(x, y, color.RGBA{r, g, b, 255})
		}
	}
	f, err := os.CreateTemp(t.TempDir(), "*.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return data
}
