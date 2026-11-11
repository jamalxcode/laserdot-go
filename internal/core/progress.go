package core

// IndexProgress: a folder being indexed for the first time. Phase is
// "scanning" (walking the folder; Total unknown) or "saving" (Done of Total
// entries read and stored).
type IndexProgress struct {
	Root  string `json:"root"`
	Phase string `json:"phase"`
	Done  uint64 `json:"done"`
	Total uint64 `json:"total"`
}

// Progress: the folder being indexed now, or nil.
func (ld *Laserdot) Progress() *IndexProgress { return ld.progress.Load() }
