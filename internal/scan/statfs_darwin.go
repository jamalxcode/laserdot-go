package scan

import "golang.org/x/sys/unix"

// fsInfo is what statfs says about the volume holding a path.
type fsInfo struct {
	fsType      string
	readOnly    bool
	total, free uint64
	// hidden: mounted "don't browse" (MNT_DONTBROWSE): macOS's own mounts
	// (update and developer disk images, Time Machine snapshots), which
	// Finder does not show either.
	hidden bool
	// device: what is mounted (/dev/disk3s3), to tell the startup disk's
	// own volumes (Recovery) from drives.
	device string
}

func statFS(path string) (fsInfo, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return fsInfo{}, err
	}
	return fsInfo{
		fsType:   cString(st.Fstypename[:]),
		readOnly: st.Flags&unix.MNT_RDONLY != 0,
		total:    st.Blocks * uint64(st.Bsize),
		free:     st.Bavail * uint64(st.Bsize),
		// Only for the mount itself: a folder on the Data volume (mounted
		// "don't browse" by macOS) is not hidden.
		hidden: st.Flags&unix.MNT_DONTBROWSE != 0 && cString(st.Mntonname[:]) == path,
		device: cString(st.Mntfromname[:]),
	}, nil
}

func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// networkFS are the macOS network file systems (statfs f_fstypename).
var networkFS = map[string]bool{"smbfs": true, "nfs": true, "afpfs": true, "webdav": true, "cifs": true, "ftp": true}
