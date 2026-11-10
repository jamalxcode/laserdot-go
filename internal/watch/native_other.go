//go:build !darwin && !linux

package watch

import (
	"errors"

	"github.com/jamalxcode/laserdot-go/internal/scan"
)

func startNative([]WatchRoot, Options, scan.Options, *queue) (native, Backend, bool, error) {
	return nil, Poll, false, errors.ErrUnsupported
}

func currentEventID() (uint64, bool) { return 0, false }
