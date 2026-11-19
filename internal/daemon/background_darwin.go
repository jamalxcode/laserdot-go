//go:build darwin

package daemon

import (
	"syscall"

	"github.com/ebitengine/purego"
)

// From <sys/resource.h>.
const (
	iopolTypeDisk     = 0
	iopolScopeProcess = 0
	iopolThrottle     = 3
)

func stepBack() string {
	out := "nice 10"
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10); err != nil {
		out = "nice failed: " + err.Error()
	}
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return out + ", disk not throttled"
	}
	sym, err := purego.Dlsym(lib, "setiopolicy_np")
	if err != nil {
		return out + ", disk not throttled"
	}
	var setiopolicy func(iotype, scope, policy int32) int32
	purego.RegisterFunc(&setiopolicy, sym)
	if setiopolicy(iopolTypeDisk, iopolScopeProcess, iopolThrottle) != 0 {
		return out + ", disk not throttled"
	}
	return out + ", disk throttled"
}
