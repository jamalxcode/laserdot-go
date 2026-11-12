//go:build !darwin

package scan

// readDirBulk: no bulk directory reading here; readDir lists name by name.
func readDirBulk(string, int, Options) ([]Entry, []Error, bool) { return nil, nil, false }
