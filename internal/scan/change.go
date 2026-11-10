package scan

// Change is "something under Path changed" — produced by the file watchers
// (internal/watch) and consumed by the index update engine. Recursive means
// the whole subtree must be rescanned (events were dropped or coalesced).
type Change struct {
	Path      string
	Recursive bool
}
