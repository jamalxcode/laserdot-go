//go:build !darwin && !linux

package daemon

func physicalMemory() uint64 { return 0 }
