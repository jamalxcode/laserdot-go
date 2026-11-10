//go:build darwin || linux

package scan

import (
	"runtime"

	"golang.org/x/sys/unix"
)

// Finder tags are read on macOS only, as in the Rust build.
var finderTagsSupported = runtime.GOOS == "darwin"

// getXattr reads an extended attribute without following symlinks.
func getXattr(path, name string) ([]byte, bool) {
	size, err := unix.Lgetxattr(path, name, nil)
	if err != nil || size <= 0 || size > maxXattrSize {
		return nil, false
	}
	buf := make([]byte, size)
	got, err := unix.Lgetxattr(path, name, buf)
	if err != nil || got < 0 {
		return nil, false
	}
	return buf[:min(got, len(buf))], true
}
