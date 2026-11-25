//go:build !darwin && !linux

package scan

import "errors"

type fsInfo struct {
	fsType      string
	readOnly    bool
	total, free uint64
	hidden      bool   // macOS only (MNT_DONTBROWSE)
	device      string // macOS only
}

func statFS(string) (fsInfo, error) { return fsInfo{}, errors.ErrUnsupported }

var networkFS = map[string]bool{}
