package scan

import "golang.org/x/sys/unix"

// fsInfo is what statfs says about the volume holding a path.
type fsInfo struct {
	fsType      string
	readOnly    bool
	total, free uint64
}

func statFS(path string) (fsInfo, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return fsInfo{}, err
	}
	name := st.Fstypename[:]
	for i, c := range name {
		if c == 0 {
			name = name[:i]
			break
		}
	}
	return fsInfo{
		fsType:   string(name),
		readOnly: st.Flags&unix.MNT_RDONLY != 0,
		total:    st.Blocks * uint64(st.Bsize),
		free:     st.Bavail * uint64(st.Bsize),
	}, nil
}

// networkFS are the macOS network file systems (statfs f_fstypename).
var networkFS = map[string]bool{"smbfs": true, "nfs": true, "afpfs": true, "webdav": true, "cifs": true, "ftp": true}
