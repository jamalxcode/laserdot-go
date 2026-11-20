//go:build darwin

package media

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A HEIC (every iPhone photo) gets a fingerprint through ImageIO, close to
// the same picture's JPEG one, so the two are found as similar.
func TestHEICGetsAFingerprintCloseToItsJPEG(t *testing.T) {
	dir := t.TempDir()
	jpg := filepath.Join("testdata", "base.jpg")
	heic := filepath.Join(dir, "base.heic")
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
	gh, ok := ImageGray(heic, "heic")
	if !ok {
		t.Fatal("the HEIC decodes through ImageIO")
	}
	if d := Hamming(PHash(&gj), PHash(&gh)); d > 8 {
		t.Errorf("HEIC and JPEG of one picture differ by %d bits", d)
	}
	// Not a picture at all: no fingerprint, no crash.
	junk := filepath.Join(dir, "junk.heic")
	os.WriteFile(junk, []byte("not an image"), 0o644)
	if _, ok := ImageGray(junk, "heic"); ok {
		t.Error("a broken HEIC has no fingerprint")
	}
}
