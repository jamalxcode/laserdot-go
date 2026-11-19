package daemon

// StepBack makes laserdotd a background process, as Spotlight's indexer is:
// a lower CPU priority (nice 10) and, on macOS, throttled disk access, so
// the disk serves the user's apps first and indexing waits while they are
// busy. A big drive being indexed then never makes the Mac slow (the owner
// saw the beachball while an 8 TB drive was read). Searches are answered
// from memory and are not affected. Child processes (ffprobe) inherit it.
// Returns what was applied, for the log.
func StepBack() string { return stepBack() }
