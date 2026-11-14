package core

import "time"

// IndexProgress: a folder being indexed for the first time. Phase is
// "scanning" (walking the folder: Done files found so far, Total unknown),
// "saving" (Done of Total entries stored) or, once files are searchable,
// "details" (Done of Total media files' headers read in the background).
type IndexProgress struct {
	Root  string `json:"root"`
	Phase string `json:"phase"`
	Done  uint64 `json:"done"`
	Total uint64 `json:"total"`
}

// Progress: the folder being indexed now, or nil.
func (ld *Laserdot) Progress() *IndexProgress {
	if p := ld.progress.Load(); p != nil {
		return p
	}
	return ld.details.Load()
}

// Healthy reports whether the in-memory index can still be locked within
// timeout. A bug caught in the middle of a change could leave that lock
// held, and every later search would then wait forever; the daemon exits
// instead (the app starts a fresh one). The writers' lock is not checked: a
// long index holds it for minutes, rightly.
func (ld *Laserdot) Healthy(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if ld.mu.TryLock() {
			ld.mu.Unlock()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}
