package scan

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

type stat struct {
	inode, dev, nlink uint64
	created           int64
	dataless          bool
}

func statOf(path string, info os.FileInfo) stat {
	var s stat
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		s.inode, s.dev, s.nlink = st.Ino, uint64(st.Dev), uint64(st.Nlink)
	}
	// Birth time needs statx (Linux 4.11+); without it, created = modified.
	var x unix.Statx_t
	if unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &x) == nil && x.Mask&unix.STATX_BTIME != 0 {
		s.created = x.Btime.Sec
	}
	return s
}

func devOf(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev)
	}
	return 0
}
