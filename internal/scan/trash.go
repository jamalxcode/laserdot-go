package scan

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Trash moves path to the Trash and returns where it went (duplicate
// cleanup, spec §4.3). It never deletes.
//
//   - macOS: the Finder's Trash — ~/.Trash for files on the home volume,
//     <volume>/.Trashes/<uid> for files on another volume — under a unique
//     name ("name 2.ext") when the name is taken.
//   - Elsewhere: the freedesktop.org Trash — $XDG_DATA_HOME/Trash (default
//     ~/.local/share/Trash) with a .trashinfo record, or
//     <top of volume>/.Trash-<uid> for files on another device.
func Trash(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if filepath.Base(abs) == "/" || filepath.Dir(abs) == abs {
		return "", errors.New("nothing to trash")
	}
	return trash(abs)
}

// volumeTop is the highest ancestor of path on device dev.
func volumeTop(path string, dev uint64) string {
	top := filepath.Dir(path)
	for {
		up := filepath.Dir(top)
		if up == top {
			return top
		}
		info, err := os.Stat(up)
		if err != nil || devOf(info) != dev {
			return top
		}
		top = up
	}
}

// numbered is name with a copy number before the extension: "name (2).ext"
// (freedesktop) or "name 2.ext" (Finder).
func numbered(name string, n int, finder bool) string {
	stem, ext := name, ""
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		stem, ext = name[:i], name[i:]
	}
	if finder {
		return stem + " " + strconv.Itoa(n) + ext
	}
	return stem + " (" + strconv.Itoa(n) + ")" + ext
}
