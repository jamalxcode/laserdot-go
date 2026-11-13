package daemon

import "runtime/debug"

// SetMemoryLimit gives the Go runtime a soft memory limit: an eighth of
// the machine's memory, at least 768 MiB. Normal indexing stays below it
// (the owner prefers speed to a small footprint); it only makes the
// collector work harder before a runaway could crowd out other apps.
func SetMemoryLimit() int64 {
	limit := int64(768 << 20)
	if total := physicalMemory(); total/8 > uint64(limit) {
		limit = int64(total / 8)
	}
	debug.SetMemoryLimit(limit)
	return limit
}
