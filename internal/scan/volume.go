package scan

import (
	"os"
	"path/filepath"
	"sort"
)

// IsNetworkVolume reports whether path is on a network file system (SMB,
// NFS, AFP, WebDAV; on Linux also CIFS/SMB2, 9p and network FUSE mounts such
// as sshfs), where change events are unreliable and polling is used instead
// (spec §2.6.6). False when it cannot tell.
func IsNetworkVolume(path string) bool {
	st, err := statFS(path)
	return err == nil && networkFS[st.fsType]
}

// Volume describes a mounted volume (spec §9, §17.3).
type Volume struct {
	Mount string
	// Name is the display name: the mount folder's name, e.g. "Archive".
	Name string
	// FSType: apfs, hfs, exfat, msdos, ntfs, ext4…
	FSType     string
	ReadOnly   bool
	TotalBytes uint64
	FreeBytes  uint64
	// Dev is the device number of the mount point.
	Dev uint64
	// StableInodes: inode numbers survive remounts. FAT and exFAT synthesize
	// them per mount, so they cannot identify files across mounts.
	StableInodes bool
}

// VolumeAt returns facts about the volume mounted at (or containing) mount.
func VolumeAt(mount string) (Volume, error) {
	info, err := os.Stat(mount)
	if err != nil {
		return Volume{}, err
	}
	st, err := statFS(mount)
	if err != nil {
		return Volume{}, err
	}
	name := filepath.Base(mount)
	if name == "/" || name == "." || name == "" {
		name = "/"
	}
	stable := true
	switch st.fsType {
	case "msdos", "exfat", "vfat", "fat", "fat32":
		stable = false
	}
	return Volume{
		Mount:        mount,
		Name:         name,
		FSType:       st.fsType,
		ReadOnly:     st.readOnly,
		TotalBytes:   st.total,
		FreeBytes:    st.free,
		Dev:          devOf(info),
		StableInodes: stable,
	}, nil
}

// ListVolumes lists the volumes mounted in dir (/Volumes on macOS), sorted
// by mount path. With mountsOnly, a folder counts only if it is a mount
// point (its device differs from dir's); tests pass false to use plain
// folders as stand-in drives. Symlinks (/Volumes/Macintosh HD → /) are
// skipped.
func ListVolumes(dir string, mountsOnly bool) []Volume {
	parent, err := os.Stat(dir)
	if err != nil {
		return nil
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Volume
	for _, de := range des {
		path := filepath.Join(dir, de.Name())
		m, err := os.Lstat(path)
		if err != nil || !m.IsDir() || (mountsOnly && devOf(m) == devOf(parent)) {
			continue
		}
		if path, err = filepath.EvalSymlinks(path); err != nil {
			continue
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		if v, err := VolumeAt(path); err == nil {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mount < out[j].Mount })
	return out
}

// Estimate counts what indexing mount would cover — files, folders and
// bytes — without writing anything (the first-mount prompt, spec §9.3).
func Estimate(mount string, opts Options) (files, dirs, bytes uint64, err error) {
	entries, _, err := Walk(mount, opts)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, e := range entries {
		if e.IsDir {
			dirs++
		} else {
			files++
		}
		bytes += e.Size
	}
	return files, dirs, bytes, nil
}
