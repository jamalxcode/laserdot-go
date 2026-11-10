//go:build !darwin

package scan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func trash(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	dev := devOf(info)
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("no home folder for the Trash")
		}
		data = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(data, "Trash")
	if err := mkTrash(dir); err != nil {
		return "", err
	}
	if ti, err := os.Stat(dir); err != nil || devOf(ti) != dev {
		dir = filepath.Join(volumeTop(path, dev), fmt.Sprintf(".Trash-%d", os.Getuid()))
		if err := mkTrash(dir); err != nil {
			return "", err
		}
	}
	name := filepath.Base(path)
	// Reserve a unique name by creating its .trashinfo exclusively.
	target := name
	var f *os.File
	for n := 2; ; n++ {
		f, err = os.OpenFile(filepath.Join(dir, "info", target+".trashinfo"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			break
		}
		if !os.IsExist(err) || n > 10_000 {
			return "", err
		}
		target = numbered(name, n, false)
	}
	infoPath := f.Name()
	_, err = fmt.Fprintf(f, "[Trash Info]\nPath=%s\nDeletionDate=%s\n", percentEncode(path), time.Now().Format("2006-01-02T15:04:05"))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	dest := filepath.Join(dir, "files", target)
	if err == nil {
		if _, serr := os.Lstat(dest); serr == nil {
			err = &os.LinkError{Op: "trash", Old: path, New: dest, Err: os.ErrExist}
		} else {
			err = os.Rename(path, dest)
		}
	}
	if err != nil {
		_ = os.Remove(infoPath)
		return "", err
	}
	return dest, nil
}

func mkTrash(dir string) error {
	for _, sub := range []string{"files", "info"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// percentEncode escapes a path for a .trashinfo Path= line (RFC 2396).
func percentEncode(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("/-_.~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
