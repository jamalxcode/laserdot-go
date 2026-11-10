//go:build !darwin && !linux

package scan

var finderTagsSupported = false

func getXattr(string, string) ([]byte, bool) { return nil, false }
