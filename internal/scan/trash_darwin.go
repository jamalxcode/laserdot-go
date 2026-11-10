package scan

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

func trash(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	dev := devOf(info)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("no home folder for the Trash")
	}
	dir := filepath.Join(home, ".Trash")
	if hi, err := os.Stat(home); err != nil || devOf(hi) != dev {
		// Another volume: its own <volume>/.Trashes/<uid>, as the Finder does.
		top := volumeTop(path, dev)
		trashes := filepath.Join(top, ".Trashes")
		if err := os.Mkdir(trashes, 0o333|os.ModeSticky); err != nil && !os.IsExist(err) {
			return "", err
		}
		dir = filepath.Join(trashes, strconv.Itoa(os.Getuid()))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := filepath.Base(path)
	target := name
	for n := 2; ; n++ {
		dest := filepath.Join(dir, target)
		// RENAME_EXCL: never replace something already in the Trash.
		err := unix.RenamexNp(path, dest, unix.RENAME_EXCL)
		if err == nil {
			return dest, nil
		}
		if !errors.Is(err, unix.EEXIST) || n > 10_000 {
			return "", &os.LinkError{Op: "trash", Old: path, New: dest, Err: err}
		}
		target = numbered(name, n, true)
	}
}
