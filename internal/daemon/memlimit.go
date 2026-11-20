package daemon

import "runtime/debug"

// SetMemoryLimit gives the Go runtime a soft memory limit: a quarter of
// the machine's memory, at least 1.5 GiB. Normal indexing stays below it
// (the owner prefers speed to a small footprint: "use more memory if
// needed"); it only makes the collector work harder before a runaway could
// crowd out other apps. It was an eighth (896 MB on CI's Mac), and a first
// index of a million files peaked at 883 MB: right at the limit, where the
// collector runs nearly all the time (15 s to searchable instead of about 7).
// Memory is returned once the index is built (68 MB after, measured).
func SetMemoryLimit() int64 {
	limit := int64(1536 << 20)
	if total := physicalMemory(); total/4 > uint64(limit) {
		limit = int64(total / 4)
	}
	debug.SetMemoryLimit(limit)
	return limit
}
