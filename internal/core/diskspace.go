package core

import (
	"fmt"
	"path/filepath"
	"syscall"
)

// MinFreeBytes: below this much free space where the index lives (or on a
// drive being indexed), indexing does not start: SQLite could fail halfway
// with "disk full" and macOS itself misbehaves when a disk is nearly full.
var MinFreeBytes uint64 = 512 << 20

// diskFree: the bytes free for this user on the volume holding path (a
// variable so tests can pretend a disk is full).
var diskFree = func(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// LowDiskError: there is not enough free space to start.
type LowDiskError struct {
	Path string
	Free uint64
}

func (e *LowDiskError) Error() string {
	return fmt.Sprintf("only %d MB free on the disk holding %s: free up some space (at least %d MB), then try again",
		e.Free>>20, e.Path, MinFreeBytes>>20)
}

// enoughSpace checks each path's volume; nil when free space is unknown.
func enoughSpace(paths ...string) error {
	for _, p := range paths {
		free, err := diskFree(p)
		if err != nil {
			continue
		}
		if free < MinFreeBytes {
			return &LowDiskError{Path: p, Free: free}
		}
	}
	return nil
}

func (ld *Laserdot) dbDir() string { return filepath.Dir(ld.dbPath) }
