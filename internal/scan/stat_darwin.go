package scan

import (
	"os"
	"syscall"
)

// sfDataless marks an iCloud placeholder whose data is not on disk (§11).
const sfDataless = 0x40000000

type stat struct {
	inode, dev, nlink uint64
	created           int64
	dataless          bool
}

func statOf(_ string, info os.FileInfo) stat {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return stat{}
	}
	return stat{
		inode:    st.Ino,
		dev:      uint64(st.Dev),
		nlink:    uint64(st.Nlink),
		created:  st.Birthtimespec.Sec,
		dataless: st.Flags&sfDataless != 0,
	}
}

func devOf(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev)
	}
	return 0
}
