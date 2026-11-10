package scan

// Change says "something changed under Path" (Recursive: the whole subtree
// may have changed). Temporary until the platform layer lands.
type Change struct {
	Path      string
	Recursive bool
}
