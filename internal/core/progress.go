package core

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
