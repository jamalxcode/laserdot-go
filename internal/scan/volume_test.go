package scan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDescribesAVolume(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	v, err := VolumeAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if v.TotalBytes == 0 || v.FreeBytes > v.TotalBytes || v.FSType == "" || v.Dev == 0 && v.Name == "" {
		t.Errorf("%+v", v)
	}
	if err := os.MkdirAll(filepath.Join(dir, "vols/Archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "vols/not-a-folder"), "x")
	if vs := ListVolumes(filepath.Join(dir, "vols"), true); len(vs) != 0 {
		t.Errorf("plain folders are not mount points: %+v", vs)
	}
	vs := ListVolumes(filepath.Join(dir, "vols"), false)
	if len(vs) != 1 || vs[0].Name != "Archive" || vs[0].Mount != filepath.Join(dir, "vols/Archive") {
		t.Errorf("%+v", vs)
	}
	if ListVolumes(filepath.Join(dir, "missing"), false) != nil {
		t.Error("missing folder has volumes")
	}
}

func TestEstimateCountsFilesFoldersBytes(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a/one.txt"), "hello")
	write(t, filepath.Join(dir, "two.txt"), "abc")
	files, dirs, bytes, err := Estimate(dir, DefaultOptions())
	if err != nil || files != 2 || dirs != 2 || bytes != 8 {
		t.Errorf("files %d dirs %d bytes %d err %v", files, dirs, bytes, err)
	}
}
