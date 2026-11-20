//go:build darwin

package core

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// An iPhone photo (HEIC) and a JPEG copy of the same picture are found as
// similar pictures (HEIC had no fingerprint before 0.9.27).
func TestHEICAndItsJPEGAreSimilarPictures(t *testing.T) {
	ld, root := newCore(t)
	writeAged(t, filepath.Join(root, "Photos/beach.jpg"), corpus(t, "base.jpg"))
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
