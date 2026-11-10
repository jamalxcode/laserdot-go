package media

import "encoding/binary"

// Bounds-checked big-endian readers: corrupt input yields ok == false,
// never a panic.

func u8At(b []byte, i int) (uint8, bool) {
	if i < 0 || i >= len(b) {
		return 0, false
	}
	return b[i], true
}

func u16At(b []byte, i int) (uint16, bool) {
	if i < 0 || i > len(b)-2 {
		return 0, false
	}
	return binary.BigEndian.Uint16(b[i:]), true
}

func u32At(b []byte, i int) (uint32, bool) {
	if i < 0 || i > len(b)-4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(b[i:]), true
}

func u64At(b []byte, i int) (uint64, bool) {
	if i < 0 || i > len(b)-8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(b[i:]), true
}

// The same, with a fallback value when out of range.
func u8Or(b []byte, i int, d uint8) uint8 {
	if v, ok := u8At(b, i); ok {
		return v
	}
	return d
}

func u16Or(b []byte, i int, d uint16) uint16 {
	if v, ok := u16At(b, i); ok {
		return v
	}
	return d
}

func u32Or(b []byte, i int, d uint32) uint32 {
	if v, ok := u32At(b, i); ok {
		return v
	}
	return d
}

func u64Or(b []byte, i int, d uint64) uint64 {
	if v, ok := u64At(b, i); ok {
		return v
	}
	return d
}

// sub is b[i:j] clamped to b (empty when out of range).
func sub(b []byte, i, j int) []byte {
	if i < 0 || i > len(b) {
		return nil
	}
	if j > len(b) || j < i {
		j = len(b)
	}
	return b[i:j]
}

// from is b[i:] (empty when out of range).
func from(b []byte, i int) []byte {
	if i < 0 || i > len(b) {
		return nil
	}
	return b[i:]
}

// rangeOK is b[i:i+n] when entirely in range.
func rangeOK(b []byte, i, n int) ([]byte, bool) {
	if i < 0 || n < 0 || i > len(b) || n > len(b)-i {
		return nil, false
	}
	return b[i : i+n], true
}
