//go:build !darwin

package daemon

import "syscall"

func stepBack() string {
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10); err != nil {
		return "nice failed: " + err.Error()
	}
	return "nice 10"
}
