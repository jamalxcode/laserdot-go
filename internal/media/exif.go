package media

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// A small EXIF (TIFF IFD) reader: finds the EXIF block in a JPEG, TIFF,
// PNG, WebP or HEIF/AVIF header buffer and indexes the primary image's
// fields (IFD0 with its Exif and GPS sub-IFDs) and the thumbnail's (IFD1).
// Every offset is bounds-checked; corrupt data yields fewer fields, never a
// panic.

// IFD contexts (a tag number is only meaningful within one).
const (
	ctxTIFF = iota
	ctxExif
	ctxGPS
	ctxInterop
	ctxThumb
)

type exifField struct {
	typ   uint16
	count uint32
	data  []byte
	bo    binary.ByteOrder
}

type exifData struct {
	// tiff is the TIFF block; thumbnail offsets are relative to it.
	tiff   []byte
	fields map[uint32]exifField
}

func exifKey(ctx int, tag uint16) uint32 { return uint32(ctx)<<16 | uint32(tag) }

func (e *exifData) get(ctx int, tag uint16) (exifField, bool) {
	f, ok := e.fields[exifKey(ctx, tag)]
	return f, ok
}

func (e *exifData) has(ctx int, tag uint16) bool {
	_, ok := e.get(ctx, tag)
	return ok
}

func (e *exifData) uintOf(ctx int, tag uint16) (uint32, bool) {
	f, ok := e.get(ctx, tag)
	if !ok {
		return 0, false
	}
	return f.uint(0)
}

// text is the first string of an ASCII field, trimmed ("" if none).
func (e *exifData) text(ctx int, tag uint16) string {
	f, ok := e.get(ctx, tag)
	if !ok || f.typ != 2 {
		return ""
	}
	d := f.data
	if i := bytes.IndexByte(d, 0); i >= 0 {
		d = d[:i]
	}
	return strings.TrimSpace(lossy(d))
}

// rational is the first value of a RATIONAL field when finite and
// positive, else 0.
func (e *exifData) rational(ctx int, tag uint16) float32 {
	f, ok := e.get(ctx, tag)
	if !ok || f.typ != 5 || len(f.data) < 8 {
		return 0
	}
	num, den := f.bo.Uint32(f.data), f.bo.Uint32(f.data[4:])
	if den == 0 || num == 0 {
		return 0
	}
	v := float32(float64(num) / float64(den))
	if v != v || v <= 0 || v > 3.4e38 {
		return 0
	}
	return v
}

// uint is value i of a BYTE, SHORT or LONG field.
func (f exifField) uint(i int) (uint32, bool) {
	if i < 0 || uint32(i) >= f.count {
		return 0, false
	}
	switch f.typ {
	case 1:
		if i < len(f.data) {
			return uint32(f.data[i]), true
		}
	case 3:
		if b, ok := rangeOK(f.data, i*2, 2); ok {
			return uint32(f.bo.Uint16(b)), true
		}
	case 4:
		if b, ok := rangeOK(f.data, i*4, 4); ok {
			return f.bo.Uint32(b), true
		}
	}
	return 0, false
}

var exifTypeSize = map[uint16]int{1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 6: 1, 7: 1, 8: 2, 9: 4, 10: 8, 11: 4, 12: 8, 13: 4}

// parseTIFF indexes the IFDs of a TIFF block (nil when it is not one).
func parseTIFF(t []byte) *exifData {
	if len(t) < 8 {
		return nil
	}
	var bo binary.ByteOrder
	switch string(t[:4]) {
	case "II*\x00":
		bo = binary.LittleEndian
	case "MM\x00*":
		bo = binary.BigEndian
	default:
		return nil
	}
	e := &exifData{tiff: t, fields: map[uint32]exifField{}}
	visited := map[uint32]bool{}
	var walk func(off uint32, ctx int, depth int) uint32
	walk = func(off uint32, ctx int, depth int) uint32 {
		if depth > 4 || visited[off] || off < 8 {
			return 0
		}
		visited[off] = true
		c, ok := rangeOK(t, int(off), 2)
		if !ok {
			return 0
		}
		n := int(bo.Uint16(c))
		for i := range n {
			ent, ok := rangeOK(t, int(off)+2+i*12, 12)
			if !ok {
				return 0
			}
			tag, typ, count := bo.Uint16(ent), bo.Uint16(ent[2:]), bo.Uint32(ent[4:])
			size, known := exifTypeSize[typ]
			if !known {
				continue
			}
			total := uint64(size) * uint64(count)
			var data []byte
			if total <= 4 {
				data = ent[8 : 8+total]
			} else {
				vo := bo.Uint32(ent[8:])
				if total > uint64(len(t)) {
					continue
				}
				d, ok := rangeOK(t, int(vo), int(total))
				if !ok {
					continue
				}
				data = d
			}
			field := exifField{typ: typ, count: count, data: data, bo: bo}
			k := exifKey(ctx, tag)
			if _, dup := e.fields[k]; !dup {
				e.fields[k] = field
			}
			if ctx == ctxTIFF || ctx == ctxExif {
				var sub int
				switch {
				case ctx == ctxTIFF && tag == 0x8769:
					sub = ctxExif
				case ctx == ctxTIFF && tag == 0x8825:
					sub = ctxGPS
				case ctx == ctxExif && tag == 0xA005:
					sub = ctxInterop
				default:
					continue
				}
				if p, ok := field.uint(0); ok {
					walk(p, sub, depth+1)
				}
			}
		}
		var next uint32
		if b, ok := rangeOK(t, int(off)+2+n*12, 4); ok {
			next = bo.Uint32(b)
		}
		return next
	}
	ifd1 := walk(bo.Uint32(t[4:]), ctxTIFF, 0)
	if ifd1 != 0 {
		walk(ifd1, ctxThumb, 0)
	}
	return e
}

// thumbnail is the embedded JPEG thumbnail (IFD1), or nil.
func (e *exifData) thumbnail() []byte {
	off, ok1 := e.uintOf(ctxThumb, 0x201)
	l, ok2 := e.uintOf(ctxThumb, 0x202)
	if !ok1 || !ok2 {
		return nil
	}
	b, ok := rangeOK(e.tiff, int(off), int(l))
	if !ok {
		return nil
	}
	return b
}

// findExif locates and parses the EXIF block of an image header buffer.
func findExif(buf []byte) *exifData {
	switch {
	case bytes.HasPrefix(buf, []byte("\xFF\xD8")):
		return parseTIFF(jpegExif(buf))
	case bytes.HasPrefix(buf, []byte("II*\x00")) || bytes.HasPrefix(buf, []byte("MM\x00*")):
		return parseTIFF(buf)
	case bytes.HasPrefix(buf, []byte("\x89PNG\r\n\x1a\n")):
		for p := 8; p+8 <= len(buf); {
			l := int(binary.BigEndian.Uint32(buf[p:]))
			typ := string(buf[p+4 : p+8])
			body, ok := rangeOK(buf, p+8, l)
			if !ok {
				return nil
			}
			if typ == "eXIf" {
				return parseTIFF(body)
			}
			if typ == "IDAT" || typ == "IEND" {
				return nil
			}
			p += 12 + l
		}
	case len(buf) >= 12 && string(buf[:4]) == "RIFF" && string(buf[8:12]) == "WEBP":
		for p := 12; p+8 <= len(buf); {
			typ := string(buf[p : p+4])
			l := int(binary.LittleEndian.Uint32(buf[p+4:]))
			body, ok := rangeOK(buf, p+8, l)
			if !ok {
				return nil
			}
			if typ == "EXIF" {
				return parseTIFF(bytes.TrimPrefix(body, []byte("Exif\x00\x00")))
			}
			p += 8 + l + l&1
		}
	case len(buf) >= 8 && string(buf[4:8]) == "ftyp":
		return parseTIFF(heifExif(buf))
	}
	return nil
}

// jpegExif is the TIFF block of the first APP1 "Exif" segment.
func jpegExif(b []byte) []byte {
	p := 2
	for p+4 <= len(b) {
		if b[p] != 0xFF {
			return nil
		}
		m := b[p+1]
		if m == 0xFF { // fill byte
			p++
			continue
		}
		if m == 0x01 || (m >= 0xD0 && m <= 0xD8) {
			p += 2
			continue
		}
		if m == 0xDA || m == 0xD9 {
			return nil
		}
		l := int(binary.BigEndian.Uint16(b[p+2:]))
		seg, ok := rangeOK(b, p+4, l-2)
		if !ok {
			return nil
		}
		if m == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return seg[6:]
		}
		p += 2 + l
	}
	return nil
}

// heifExif is the TIFF block of the HEIF/AVIF "Exif" item, when its data
// lies within buf.
func heifExif(buf []byte) []byte {
	meta, ok := child(buf, "meta")
	if !ok || len(meta) < 4 {
		return nil
	}
	meta = meta[4:]
	var exifID uint32
	found := false
	if iinf, ok := child(meta, "iinf"); ok && len(iinf) >= 4 {
		start := 6
		if iinf[0] != 0 {
			start = 8
		}
		for k, infe := range boxes(from(iinf, start)) {
			if string(k[:]) != "infe" || len(infe) < 4 {
				continue
			}
			v := infe[0]
			if v < 2 {
				continue
			}
			var id uint32
			p := 4
			if v == 2 {
				id = uint32(u16Or(infe, p, 0))
				p += 2
			} else {
				id = u32Or(infe, p, 0)
				p += 4
			}
			p += 2 // protection index
			if t, ok := rangeOK(infe, p, 4); ok && string(t) == "Exif" {
				exifID, found = id, true
				break
			}
		}
	}
	if !found {
		return nil
	}
	iloc, ok := child(meta, "iloc")
	if !ok || len(iloc) < 8 {
		return nil
	}
	v := iloc[0]
	offSize, lenSize := int(iloc[4]>>4), int(iloc[4]&15)
	baseSize, idxSize := int(iloc[5]>>4), int(iloc[5]&15)
	if v == 0 {
		idxSize = 0
	}
	p := 6
	readN := func(n int) (uint64, bool) {
		var x uint64
		for range n {
			c, ok := u8At(iloc, p)
			if !ok {
				return 0, false
			}
			x = x<<8 | uint64(c)
			p++
		}
		return x, true
	}
	var count uint64
	if v < 2 {
		count, ok = readN(2)
	} else {
		count, ok = readN(4)
	}
	if !ok {
		return nil
	}
	for range min(count, 4096) {
		var id uint64
		if v < 2 {
			id, ok = readN(2)
		} else {
			id, ok = readN(4)
		}
		if !ok {
			return nil
		}
		method := uint64(0)
		if v == 1 || v == 2 {
			if method, ok = readN(2); !ok {
				return nil
			}
			method &= 15
		}
		readN(2) // data reference index
		base, _ := readN(baseSize)
		extents, ok := readN(2)
		if !ok {
			return nil
		}
		var first []byte
		for e := uint64(0); e < extents; e++ {
			readN(idxSize)
			off, ok1 := readN(offSize)
			l, ok2 := readN(lenSize)
			if !ok1 || !ok2 {
				return nil
			}
			if e == 0 && method == 0 {
				first, _ = rangeOK(buf, int(min(base+off, uint64(len(buf)))), int(min(l, uint64(len(buf)))))
			}
		}
		if uint32(id) == exifID {
			if len(first) < 4 {
				return nil
			}
			skip := binary.BigEndian.Uint32(first)
			return from(first, 4+int(min(skip, uint32(len(first)))))
		}
	}
	return nil
}
