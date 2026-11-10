package scan

import (
	"encoding/binary"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Extended attributes holding tags: Finder tags (a binary plist of
// "Name\nColour" strings) and the freedesktop comma list.
const (
	finderTagsAttr = "com.apple.metadata:_kMDItemUserTags"
	xdgTagsAttr    = "user.xdg.tags"
	maxXattrSize   = 1 << 20
)

// ReadTags returns path's Finder tags (macOS,
// com.apple.metadata:_kMDItemUserTags) or freedesktop tags (user.xdg.tags,
// comma-separated), with Finder's colour suffixes removed. Symlinks are not
// followed. Nil when there are none.
func ReadTags(path string) []string {
	var tags []string
	if finderTagsSupported {
		if b, ok := getXattr(path, finderTagsAttr); ok {
			tags, _ = bplistStrings(b)
		}
	}
	if len(tags) == 0 {
		if b, ok := getXattr(path, xdgTagsAttr); ok {
			tags = strings.Split(toValidUTF8(b), ",")
		}
	}
	var out []string
	for _, t := range tags {
		if i := strings.IndexByte(t, '\n'); i >= 0 {
			t = t[:i]
		}
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func toValidUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "�")
}

// bplistStrings returns the strings of a binary property list whose top
// object is an array of strings — all a Finder tag list is. Non-string
// members are skipped; ok is false on anything else. It never panics and
// bounds all work by the input size: every offset is checked before use.
func bplistStrings(b []byte) (out []string, ok bool) {
	if len(b) < 40 || string(b[:8]) != "bplist00" {
		return nil, false
	}
	t := b[len(b)-32:]
	offSize := int(t[6])
	refSize := int(t[7])
	num := binary.BigEndian.Uint64(t[8:16])
	top := binary.BigEndian.Uint64(t[16:24])
	table := binary.BigEndian.Uint64(t[24:32])
	if offSize < 1 || offSize > 8 || refSize < 1 || refSize > 8 || num > 100_000 || table >= uint64(len(b)) {
		return nil, false
	}
	n := uint64(len(b))
	// readUint reads a size-byte big-endian unsigned integer at at.
	readUint := func(at, size uint64) (uint64, bool) {
		if at > n || size > n-at {
			return 0, false
		}
		var v uint64
		for _, x := range b[at : at+size] {
			v = v<<8 | uint64(x)
		}
		return v, true
	}
	offset := func(obj uint64) (uint64, bool) {
		if obj >= num {
			return 0, false
		}
		return readUint(table+obj*uint64(offSize), uint64(offSize)) // num ≤ 1e5: no overflow
	}
	// length of the object at at: the marker's low nibble, or a following
	// int object when it is 0xF. Returns the length and where data starts.
	length := func(at uint64) (uint64, uint64, bool) {
		if at >= n {
			return 0, 0, false
		}
		m := b[at]
		if m&0x0F != 0x0F {
			return uint64(m & 0x0F), at + 1, true
		}
		if at+1 >= n || b[at+1]>>4 != 0x1 {
			return 0, 0, false
		}
		size := uint64(1) << (b[at+1] & 0x0F)
		if size > 8 {
			return 0, 0, false
		}
		v, ok := readUint(at+2, size)
		return v, at + 2 + size, ok
	}
	arr, ok := offset(top)
	if !ok || arr >= n || b[arr]>>4 != 0xA {
		return nil, false
	}
	count, at, ok := length(arr)
	if !ok {
		return nil, false
	}
	for i := uint64(0); i < min(count, 4096); i++ {
		obj, ok := readUint(at, uint64(refSize))
		if !ok {
			return nil, false
		}
		at += uint64(refSize)
		o, ok := offset(obj)
		if !ok {
			return nil, false
		}
		l, data, ok := length(o)
		if !ok || o >= n {
			return nil, false
		}
		switch b[o] >> 4 {
		case 0x5: // ASCII string
			if data > n || l > n-data {
				return nil, false
			}
			out = append(out, toValidUTF8(b[data:data+l]))
		case 0x6: // UTF-16BE string
			if data > n || l > (n-data)/2 {
				return nil, false
			}
			units := make([]uint16, l)
			for j := range units {
				units[j] = binary.BigEndian.Uint16(b[data+2*uint64(j):])
			}
			out = append(out, string(utf16.Decode(units)))
		}
	}
	return out, true
}
