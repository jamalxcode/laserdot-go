package media

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"strconv"
	"strings"
)

// Images: dimensions from the header and EXIF, reading at most the first
// MiB for EXIF.

const exifWindow = 1 << 20

func parseImage(path, ext string, raw bool) *Record {
	fh, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return nil
	}
	return imageFrom(file{fh, st.Size()}, ext, raw)
}

func imageContainer(ext string) string {
	switch ext {
	case "jpg", "jpeg":
		return "jpeg"
	case "heic", "heif":
		return "heif"
	case "tif":
		return "tiff"
	}
	return ext
}

func imageFrom(f file, ext string, raw bool) *Record {
	rec := &Record{Container: imageContainer(ext)}
	if !raw {
		if w, h, ok := imageSize(f); ok {
			rec.Width, rec.Height = w, h
		}
	}
	buf := f.at(0, exifWindow)
	if ex := findExif(buf); ex != nil {
		if rec.Width == 0 {
			w, okw := ex.get(ctxExif, 0xA002)
			h, okh := ex.get(ctxExif, 0xA003)
			if okw && okh {
				rec.Width, _ = w.uint(0)
				rec.Height, _ = h.uint(0)
			}
		}
		// EXIF orientations 5–8 are rotated by 90°: report display size.
		if o, ok := ex.uintOf(ctxTIFF, 0x112); ok && o >= 5 && o <= 8 {
			rec.Width, rec.Height = rec.Height, rec.Width
		}
		make_ := ex.text(ctxTIFF, 0x10F)
		model := ex.text(ctxTIFF, 0x110)
		camera := ""
		switch {
		case make_ != "" && model != "" && strings.HasPrefix(strings.ToLower(model), strings.ToLower(make_)):
			camera = model
		case make_ != "" && model != "":
			camera = make_ + " " + model
		case model != "":
			camera = model
		default:
			camera = make_
		}
		screenshot := false
		if uc, ok := ex.get(ctxExif, 0x9286); ok && uc.typ == 7 {
			screenshot = strings.Contains(lossy(uc.data), "Screenshot")
		}
		x := &Exif{
			Camera:     camera,
			Lens:       ex.text(ctxExif, 0xA434),
			Aperture:   ex.rational(ctxExif, 0x829D),
			Focal:      ex.rational(ctxExif, 0x920A),
			GPS:        ex.has(ctxGPS, 0x0002),
			Screenshot: screenshot,
		}
		x.ISO, _ = ex.uintOf(ctxExif, 0x8827)
		taken := ex.text(ctxExif, 0x9003)
		if taken == "" {
			taken = ex.text(ctxTIFF, 0x132)
		}
		x.Taken, x.HasTaken = exifTime(taken)
		rec.Exif = x
	}
	if rec.Width == 0 && rec.Exif == nil {
		return nil
	}
	return rec
}

// exifTime converts "YYYY:MM:DD HH:MM:SS" to Unix seconds (the camera's
// local time, read as UTC).
func exifTime(s string) (int64, bool) {
	n := func(i, j int) (int64, bool) {
		if j > len(s) {
			return 0, false
		}
		v, err := strconv.ParseInt(s[i:j], 10, 64)
		return v, err == nil
	}
	y, ok1 := n(0, 4)
	mo, ok2 := n(5, 7)
	d, ok3 := n(8, 10)
	if !ok1 || !ok2 || !ok3 {
		return 0, false
	}
	h, _ := n(11, 13)
	mi, _ := n(14, 16)
	sec, _ := n(17, 19)
	if y < 1900 || mo < 1 || mo > 12 || d < 1 || d > 31 {
		return 0, false
	}
	yy := y
	if mo <= 2 {
		yy--
	}
	era := yy / 400
	if yy < 0 && yy%400 != 0 {
		era--
	}
	yoe := yy - era*400
	doy := (153*((mo+9)%12)+2)/5 + d - 1
	days := era*146_097 + yoe*365 + yoe/4 - yoe/100 + doy - 719_468
	return days*86_400 + h*3600 + mi*60 + sec, true
}

// ---------------------------------------------------------------- dimensions

// imageSize reads the pixel dimensions from the header, by content.
func imageSize(f file) (uint32, uint32, bool) {
	h := f.at(0, 64)
	if len(h) < 12 {
		return 0, 0, false
	}
	le := binary.LittleEndian
	be := binary.BigEndian
	switch {
	case bytes.HasPrefix(h, []byte("\xFF\xD8\xFF")):
		return jpegSize(f)
	case bytes.HasPrefix(h, []byte("\x89PNG\r\n\x1a\n")):
		if len(h) >= 24 {
			return be.Uint32(h[16:]), be.Uint32(h[20:]), true
		}
	case bytes.HasPrefix(h, []byte("GIF8")):
		return uint32(le.Uint16(h[6:])), uint32(le.Uint16(h[8:])), true
	case bytes.HasPrefix(h, []byte("RIFF")) && string(h[8:12]) == "WEBP":
		return webpSize(h)
	case string(h[4:8]) == "ftyp":
		return heifSize(f)
	case bytes.HasPrefix(h, []byte("II*\x00")) || bytes.HasPrefix(h, []byte("MM\x00*")):
		return tiffSize(f, string(h[:2]) == "II")
	case bytes.HasPrefix(h, []byte("BM")):
		if len(h) >= 26 {
			w := int32(le.Uint32(h[18:]))
			ht := int32(le.Uint32(h[22:]))
			return abs32(w), abs32(ht), true
		}
	case bytes.HasPrefix(h, []byte("8BPS")):
		if len(h) >= 22 {
			return be.Uint32(h[18:]), be.Uint32(h[14:]), true
		}
	case bytes.HasPrefix(h, []byte("DDS ")):
		if len(h) >= 20 {
			return le.Uint32(h[16:]), le.Uint32(h[12:]), true
		}
	case bytes.HasPrefix(h, []byte("qoif")):
		if len(h) >= 12 {
			return be.Uint32(h[4:]), be.Uint32(h[8:]), true
		}
	case bytes.HasPrefix(h, []byte("\x76\x2f\x31\x01")):
		return exrSize(f)
	case bytes.HasPrefix(h, []byte("#?RADIANCE")) || bytes.HasPrefix(h, []byte("#?RGBE")):
		return hdrSize(f)
	case bytes.HasPrefix(h, []byte("\xFF\x0A")):
		return jxlSize(h)
	case bytes.HasPrefix(h, []byte("\x00\x00\x00\x0CJXL \x0D\x0A\x87\x0A")):
		return jxlContainerSize(f)
	case looksTGA(h):
		return uint32(le.Uint16(h[12:])), uint32(le.Uint16(h[14:])), true
	}
	return 0, 0, false
}

func jpegSize(f file) (uint32, uint32, bool) {
	pos := int64(2)
	depth := 0
	for range 1 << 16 {
		m, ok := f.exact(pos, 2)
		if !ok || m[0] != 0xFF {
			return 0, 0, false
		}
		page := m[1]
		pos += 2
		isSOF := (page >= 0xC0 && page <= 0xC3) || (page >= 0xC5 && page <= 0xC7) ||
			(page >= 0xC9 && page <= 0xCB) || (page >= 0xCD && page <= 0xCF)
		if isSOF && depth == 0 {
			b, ok := f.exact(pos+3, 4)
			if !ok {
				return 0, 0, false
			}
			return uint32(binary.BigEndian.Uint16(b[2:])), uint32(binary.BigEndian.Uint16(b[:2])), true
		}
		switch page {
		case 0xD8:
			depth++
		case 0xD9:
			depth--
			if depth < 0 {
				return 0, 0, false
			}
		}
		l, ok := f.exact(pos, 2)
		if !ok {
			return 0, 0, false
		}
		pos += int64(binary.BigEndian.Uint16(l))
	}
	return 0, 0, false
}

func webpSize(h []byte) (uint32, uint32, bool) {
	if len(h) < 30 {
		return 0, 0, false
	}
	le := binary.LittleEndian
	switch string(h[12:16]) {
	case "VP8 ":
		return uint32(le.Uint16(h[26:]) & 0x3fff), uint32(le.Uint16(h[28:]) & 0x3fff), true
	case "VP8L":
		b := le.Uint32(h[21:])
		return b&0x3fff + 1, (b>>14)&0x3fff + 1, true
	case "VP8X":
		w := uint32(h[24]) | uint32(h[25])<<8 | uint32(h[26])<<16
		ht := uint32(h[27]) | uint32(h[28])<<8 | uint32(h[29])<<16
		return w + 1, ht + 1, true
	}
	return 0, 0, false
}

func tiffSize(f file, little bool) (uint32, uint32, bool) {
	var bo binary.ByteOrder = binary.BigEndian
	if little {
		bo = binary.LittleEndian
	}
	h, ok := f.exact(4, 4)
	if !ok {
		return 0, 0, false
	}
	off := int64(bo.Uint32(h))
	if off == 0 {
		return 0, 0, false
	}
	c, ok := f.exact(off, 2)
	if !ok {
		return 0, 0, false
	}
	n := int(bo.Uint16(c))
	ents, ok := f.exact(off+2, n*12)
	if !ok {
		ents = f.at(off+2, n*12)
	}
	var w, ht uint32
	var hw, hh bool
	for i := 0; i+12 <= len(ents); i += 12 {
		e := ents[i : i+12]
		tag, typ := bo.Uint16(e), bo.Uint16(e[2:])
		var v uint32
		var has bool
		switch typ {
		case 3, 8:
			v, has = uint32(bo.Uint16(e[8:])), true
		case 4, 9, 11, 13:
			v, has = bo.Uint32(e[8:]), true
		case 1, 2, 6, 7, 5, 10, 12, 16, 17, 18:
		default:
			return 0, 0, false
		}
		switch tag {
		case 0x100:
			w, hw = v, has
		case 0x101:
			ht, hh = v, has
		}
		if hw && hh {
			return w, ht, true
		}
	}
	return 0, 0, false
}

// heifSize: the largest ispe (by area) in meta/iprp/ipco, turned by irot.
func heifSize(f file) (uint32, uint32, bool) {
	buf := f.at(0, exifWindow)
	meta, ok := child(buf, "meta")
	if !ok || len(meta) < 4 {
		return 0, 0, false
	}
	iprp, ok := child(meta[4:], "iprp")
	if !ok {
		return 0, 0, false
	}
	ipco, ok := child(iprp, "ipco")
	if !ok {
		return 0, 0, false
	}
	var w, h uint32
	found := false
	rotation := uint8(0)
	for k, body := range boxes(ipco) {
		switch string(k[:]) {
		case "ispe":
			bw, ok1 := u32At(body, 4)
			bh, ok2 := u32At(body, 8)
			if !ok1 || !ok2 {
				continue
			}
			found = true
			if uint64(bw)*uint64(bh) > uint64(w)*uint64(h) {
				w, h = bw, bh
			}
		case "irot":
			rotation = u8Or(body, 0, 0)
		}
	}
	if !found {
		return 0, 0, false
	}
	if rotation == 1 || rotation == 3 {
		w, h = h, w
	}
	return w, h, true
}

func exrSize(f file) (uint32, uint32, bool) {
	b := f.at(8, 64<<10)
	for p := 0; p < len(b); {
		z := bytes.IndexByte(b[p:], 0)
		if z <= 0 {
			return 0, 0, false
		}
		name := string(b[p : p+z])
		p += z + 1
		z = bytes.IndexByte(b[p:], 0)
		if z < 0 {
			return 0, 0, false
		}
		p += z + 1
		if p+4 > len(b) {
			return 0, 0, false
		}
		size := int(binary.LittleEndian.Uint32(b[p:]))
		p += 4
		if size < 0 || size > len(b)-p {
			return 0, 0, false
		}
		if name == "dataWindow" && size >= 16 {
			v := b[p:]
			x0 := int64(int32(binary.LittleEndian.Uint32(v)))
			y0 := int64(int32(binary.LittleEndian.Uint32(v[4:])))
			x1 := int64(int32(binary.LittleEndian.Uint32(v[8:])))
			y1 := int64(int32(binary.LittleEndian.Uint32(v[12:])))
			if x1 < x0 || y1 < y0 {
				return 0, 0, false
			}
			return uint32(min(x1-x0+1, math.MaxUint32)), uint32(min(y1-y0+1, math.MaxUint32)), true
		}
		p += size
	}
	return 0, 0, false
}

func hdrSize(f file) (uint32, uint32, bool) {
	b := f.at(0, 4096)
	for _, line := range strings.Split(string(b), "\n") {
		fs := strings.Fields(line)
		if len(fs) == 4 && (fs[0] == "-Y" || fs[0] == "+Y") && (fs[2] == "+X" || fs[2] == "-X") {
			h, err1 := strconv.ParseUint(fs[1], 10, 32)
			w, err2 := strconv.ParseUint(fs[3], 10, 32)
			if err1 == nil && err2 == nil {
				return uint32(w), uint32(h), true
			}
		}
	}
	return 0, 0, false
}

// looksTGA: a plausible uncompressed/RLE TGA header (TGA has no magic).
func looksTGA(h []byte) bool {
	if len(h) < 18 {
		return false
	}
	cmapType, imgType, depth := h[1], h[2], h[16]
	if cmapType > 1 {
		return false
	}
	switch imgType {
	case 1, 2, 3, 9, 10, 11:
	default:
		return false
	}
	switch depth {
	case 8, 15, 16, 24, 32:
	default:
		return false
	}
	return binary.LittleEndian.Uint16(h[12:]) > 0 && binary.LittleEndian.Uint16(h[14:]) > 0
}

// jxlSize decodes the SizeHeader of a bare JPEG XL codestream.
func jxlSize(h []byte) (uint32, uint32, bool) {
	r := &lsbBits{b: h[2:]}
	u32 := func(dist [4]uint32, bits [4]int) uint32 {
		sel := r.read(2)
		return dist[sel] + r.read(bits[sel])
	}
	var w, ht uint32
	small := r.read(1) == 1
	if small {
		ht = (r.read(5) + 1) * 8
	} else {
		ht = u32([4]uint32{1, 1, 1, 1}, [4]int{9, 13, 18, 30})
	}
	ratio := r.read(3)
	if ratio == 0 {
		if small {
			w = (r.read(5) + 1) * 8
		} else {
			w = u32([4]uint32{1, 1, 1, 1}, [4]int{9, 13, 18, 30})
		}
	} else {
		num := [8]uint64{0, 1, 12, 4, 3, 16, 5, 2}
		den := [8]uint64{0, 1, 10, 3, 2, 9, 4, 1}
		w = uint32(min(uint64(ht)*num[ratio]/den[ratio], math.MaxUint32))
	}
	if r.err || w == 0 || ht == 0 {
		return 0, 0, false
	}
	return w, ht, true
}

// jxlContainerSize finds the codestream (jxlc, or the first jxlp) in a JPEG
// XL container.
func jxlContainerSize(f file) (uint32, uint32, bool) {
	buf := f.at(0, 64<<10)
	for k, body := range boxes(buf) {
		switch string(k[:]) {
		case "jxlc":
			if len(body) >= 2 && body[0] == 0xFF && body[1] == 0x0A {
				return jxlSize(body)
			}
		case "jxlp":
			if len(body) >= 6 && body[4] == 0xFF && body[5] == 0x0A {
				return jxlSize(body[4:])
			}
		}
	}
	return 0, 0, false
}

// lsbBits reads bits least-significant first (JPEG XL).
type lsbBits struct {
	b   []byte
	pos int
	err bool
}

func (r *lsbBits) read(n int) uint32 {
	var v uint32
	for i := range n {
		if r.pos/8 >= len(r.b) {
			r.err = true
			return 0
		}
		v |= uint32(r.b[r.pos/8]>>(r.pos%8)&1) << i
		r.pos++
	}
	return v
}
