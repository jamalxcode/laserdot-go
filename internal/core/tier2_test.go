package core

import (
	"path/filepath"
	"testing"

	"github.com/jamalxcode/laserdot-go/internal/media"
)

// Tier 2 at Seam 1: legacy containers become searchable by their media
// facts after a deep-probe pass, each file is read once, and VFR is learned
// for Matroska. Needs ffprobe (skipped without it).
func TestDeepProbeFillsWhatTier1CouldNotRead(t *testing.T) {
	if media.Tier2Available() == "" {
		t.Skip("ffprobe not installed")
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "lib")
	for _, f := range []string{"legacy.avi", "broadcast.ts", "clip.wmv", "dvd.mpg", "old.flv", "vfr.mkv"} {
		writeAged(t, filepath.Join(root, f), corpus(t, filepath.Join("tier2", f)))
	}
	ld := mustOpen(t, filepath.Join(base, "data/index.db"))
	defer ld.Close()
	ld.Index(root)
	expect(t, lnames(t, ld, "codec:mpeg4"))
	expect(t, lnames(t, ld, "vfr:true"))
	st, err := ld.DeepProbe(0)
	// broadcast.ts is under 4 MiB, so it counts as code (TypeScript), not
	// video (§3.1, as in the Rust build): five jobs.
	if err != nil || st.Probed != 5 || st.Updated != 5 {
		t.Fatalf("%+v %v", st, err)
	}
	expect(t, lnames(t, ld, "codec:mpeg4 res:sd"), "legacy.avi")
	expect(t, lnames(t, ld, "codec:mpeg2video"), "dvd.mpg")
	expect(t, lnames(t, ld, "vfr:true"), "vfr.mkv")
	// Each version is probed once; the results survive a reopen.
	if again, _ := ld.DeepProbe(0); again.Probed != 0 {
		t.Errorf("%+v", again)
	}
	ld.Close()
	ld = mustOpen(t, filepath.Join(base, "data/index.db"))
	expect(t, lnames(t, ld, "codec:wmv2"), "clip.wmv")
	expect(t, lnames(t, ld, "vfr:true"), "vfr.mkv")
}
