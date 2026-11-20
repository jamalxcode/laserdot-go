//go:build !darwin

package media

// imageIOGray: no system image decoder outside macOS.
func imageIOGray(string) (Gray, bool) { return Gray{}, false }

func loadImageIO() bool { return false }
