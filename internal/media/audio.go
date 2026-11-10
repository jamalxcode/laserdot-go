package media

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Audio files other than MP4/Matroska (MP3, FLAC, Ogg Vorbis/Opus/Speex,
// WAV, AIFF, APE, WavPack, ADTS AAC, Musepack): tags and stream properties
// from the headers, cover art skipped. The Rust build used the lofty crate;
// this is a hand-written equivalent that follows its rules (which tag is
// primary, how duration and bitrates are derived).

// Bounds on what a header parser reads into memory.
const (
	maxTextFrame  = 64 << 10 // one ID3v2 text frame
	maxTagRead    = 16 << 20 // an ID3v2 tag that must be read whole
	maxComments   = 1 << 20  // a Vorbis comment packet (pictures past it are ignored)
	syncSearch    = 256 << 10
	oggTailWindow = 128 << 10
)

// file is a sized io.ReaderAt with forgiving reads.
type file struct {
	r    io.ReaderAt
	size int64
}

// at reads up to n bytes at off (fewer at the end of the file).
func (f file) at(off int64, n int) []byte {
	if off < 0 || off >= f.size || n <= 0 {
		return nil
	}
	n = int(min(int64(n), f.size-off))
	b := make([]byte, n)
	m, _ := f.r.ReadAt(b, off)
	return b[:m]
}

// exact reads exactly n bytes at off.
func (f file) exact(off int64, n int) ([]byte, bool) {
	b := f.at(off, n)
	return b, len(b) == n && n >= 0
}

// audioTags accumulates tags; the first tag source that has any value wins
// as a whole (lofty's primary tag, then the first other tag).
type tagSet struct {
	Tags
	date string
}

func (t *tagSet) empty() bool {
	return t.Title == "" && t.Artist == "" && t.Album == "" && t.Genre == "" && t.date == ""
}

func (t *tagSet) final() Tags {
	out := Tags{
		Title:  strings.TrimSpace(t.Title),
		Artist: strings.TrimSpace(t.Artist),
		Album:  strings.TrimSpace(t.Album),
		Genre:  strings.TrimSpace(t.Genre),
	}
	out.Year = dateYear(strings.TrimSpace(t.date))
	return out
}

// dateYear: the year of a timestamp (YYYY[-MM[-DD…]]), 0 when absent.
func dateYear(s string) uint32 {
	if len(s) < 4 {
		return 0
	}
	y, err := strconv.ParseUint(s[:4], 10, 16)
	if err != nil {
		return 0
	}
	return uint32(y)
}

type audioInfo struct {
	container, codec string
	lossless         bool
	durationMS       uint64
	channels         uint8
	sampleRate       uint32
	audioKbps        uint32
	overallKbps      uint32
	primary, other   *tagSet
}

func parseAudio(path string) *Record {
	fh, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return nil
	}
	return audioFrom(file{fh, st.Size()})
}

func audioFrom(f file) *Record {
	// ID3v2 tags may precede any stream (lofty skips them everywhere).
	var id3 *tagSet
	start := int64(0)
	for range 4 {
		t, size, ok := readID3v2(f, start)
		if !ok {
			break
		}
		if id3 == nil {
			id3 = t
		}
		start += size
	}
	head := f.at(start, 64)
	if len(head) < 4 {
		return nil
	}
	var info *audioInfo
	switch {
	case bytes.HasPrefix(head, []byte("fLaC")):
		info = flacInfo(f, start)
	case bytes.HasPrefix(head, []byte("OggS")):
		info = oggInfo(f, start)
	case bytes.HasPrefix(head, []byte("RIFF")) && len(head) >= 12 && string(head[8:12]) == "WAVE":
		info = wavInfo(f, start)
	case bytes.HasPrefix(head, []byte("FORM")) && len(head) >= 12 &&
		(string(head[8:12]) == "AIFF" || string(head[8:12]) == "AIFC"):
		info = aiffInfo(f, start)
	case bytes.HasPrefix(head, []byte("MAC ")):
		info = apeInfo(f, start)
	case bytes.HasPrefix(head, []byte("wvpk")):
		info = wavpackInfo(f, start)
	case bytes.HasPrefix(head, []byte("MPCK")) || bytes.HasPrefix(head, []byte("MP+")):
		info = mpcInfo(f, start)
	case len(head) >= 8 && string(head[4:8]) == "ftyp":
		rec := isoFrom(f.r, uint64(f.size))
		if rec != nil {
			rec.Container = "m4a"
		}
		return rec
	default:
		info = mpegOrADTS(f, start)
	}
	if info == nil {
		return nil
	}
	tags := info.primary
	if tags == nil || tags.empty() {
		switch {
		case id3 != nil && !id3.empty():
			tags = id3
		case info.other != nil && !info.other.empty():
			tags = info.other
		}
	}
	rec := &Record{
		Container:  info.container,
		DurationMS: info.durationMS,
		Bitrate:    satKbps(info.overallKbps),
		Tracks: []Track{{
			Kind:       TrackAudio,
			Codec:      info.codec,
			Channels:   info.channels,
			SampleRate: info.sampleRate,
			Bitrate:    satKbps(info.audioKbps),
			Lossless:   info.lossless,
			Default:    true,
		}},
	}
	if tags != nil {
		rec.Tags = tags.final()
	}
	return rec
}

func satKbps(k uint32) uint32 {
	if k > math.MaxUint32/1000 {
		return math.MaxUint32
	}
	return k * 1000
}

// divRound is a/b rounded to nearest (0 when b is 0).
func divRound(a, b uint64) uint64 {
	if b == 0 {
		return 0
	}
	return (a + b/2) / b
}

// kbps: bits per millisecond over a duration (lofty's bitrate unit).
func kbps(byteLen uint64, ms uint64) uint32 {
	if ms == 0 {
		return 0
	}
	return uint32(min(satMul(byteLen, 8)/ms, math.MaxUint32))
}

// ---------------------------------------------------------------- ID3

func syncsafe(b []byte) uint32 {
	return uint32(b[0]&0x7f)<<21 | uint32(b[1]&0x7f)<<14 | uint32(b[2]&0x7f)<<7 | uint32(b[3]&0x7f)
}

// unsync removes ID3v2 unsynchronisation (FF 00 → FF).
func unsync(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		out = append(out, b[i])
		if b[i] == 0xFF && i+1 < len(b) && b[i+1] == 0 {
			i++
		}
	}
	return out
}

// readID3v2 reads the ID3v2 tag at off: its text tags and total size.
func readID3v2(f file, off int64) (*tagSet, int64, bool) {
	h, ok := f.exact(off, 10)
	if !ok || string(h[:3]) != "ID3" || h[3] < 2 || h[3] > 4 || h[6]|h[7]|h[8]|h[9] >= 0x80 {
		return nil, 0, false
	}
	ver, flags := h[3], h[5]
	size := int64(syncsafe(h[6:10]))
	total := 10 + size
	if ver == 4 && flags&0x10 != 0 {
		total += 10 // footer
	}
	tags := &tagSet{}
	src := f
	base := off + 10
	end := base + size
	if flags&0x80 != 0 && ver < 4 {
		// Whole-tag unsynchronisation: read it into memory (bounded).
		if size > maxTagRead {
			return tags, total, true
		}
		body := unsync(f.at(base, int(size)))
		src = file{bytes.NewReader(body), int64(len(body))}
		base, end = 0, int64(len(body))
	}
	pos := base
	if flags&0x40 != 0 && ver >= 3 {
		eh, ok := src.exact(pos, 4)
		if !ok {
			return tags, total, true
		}
		if ver == 3 {
			pos += 4 + int64(binary.BigEndian.Uint32(eh))
		} else {
			pos += int64(syncsafe(eh))
		}
	}
	idLen, hdrLen := 4, 10
	if ver == 2 {
		idLen, hdrLen = 3, 6
	}
	for pos+int64(hdrLen) <= end {
		fh, ok := src.exact(pos, hdrLen)
		if !ok || fh[0] == 0 {
			break
		}
		id := string(fh[:idLen])
		var fsize int64
		var fflags uint16
		switch ver {
		case 2:
			fsize = int64(fh[3])<<16 | int64(fh[4])<<8 | int64(fh[5])
		case 3:
			fsize = int64(binary.BigEndian.Uint32(fh[4:8]))
			fflags = binary.BigEndian.Uint16(fh[8:10])
		default:
			fsize = int64(syncsafe(fh[4:8]))
			fflags = binary.BigEndian.Uint16(fh[8:10])
		}
		bodyAt := pos + int64(hdrLen)
		if fsize < 0 || fsize > end-bodyAt {
			break
		}
		pos = bodyAt + fsize
		key := id3Key(id)
		if key == "" || fsize == 0 || fsize > maxTextFrame {
			continue
		}
		// Compressed or encrypted frames are skipped.
		compressed := (ver == 3 && fflags&0x00C0 != 0) || (ver == 4 && fflags&0x000C != 0)
		if compressed {
			continue
		}
		body, ok := src.exact(bodyAt, int(fsize))
		if !ok {
			break
		}
		if ver == 4 {
			if fflags&0x0001 != 0 && len(body) >= 4 { // data length indicator
				body = body[4:]
			}
			if fflags&0x0002 != 0 || flags&0x80 != 0 {
				body = unsync(body)
			}
		}
		text := id3Text(body)
		switch key {
		case "title":
			setIfEmpty(&tags.Title, firstValue(text))
		case "artist":
			setIfEmpty(&tags.Artist, firstValue(text))
		case "album":
			setIfEmpty(&tags.Album, firstValue(text))
		case "genre":
			setIfEmpty(&tags.Genre, id3Genre(text))
		case "date":
			if id == "TDRC" || tags.date == "" {
				tags.date = firstValue(text)
			}
		}
	}
	return tags, total, true
}

func setIfEmpty(dst *string, v string) {
	if *dst == "" {
		*dst = v
	}
}

func id3Key(id string) string {
	switch id {
	case "TIT2", "TT2":
		return "title"
	case "TPE1", "TP1":
		return "artist"
	case "TALB", "TAL":
		return "album"
	case "TCON", "TCO":
		return "genre"
	case "TDRC", "TYER", "TYE":
		return "date"
	}
	return ""
}

func firstValue(s string) string {
	if i := strings.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	return s
}

// id3Text decodes a text frame (encoding byte + text), trailing NULs
// removed; ID3v2.4 multiple values stay NUL-separated.
func id3Text(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	enc, b := b[0], b[1:]
	var s string
	switch enc {
	case 0:
		s = latin1(b)
	case 1, 2:
		s = utf16Text(b, enc == 2)
	default:
		s = lossy(b)
	}
	return strings.TrimRight(s, "\x00")
}

func latin1(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

// utf16Text decodes UTF-16 text, values separated by 00 00 and each
// optionally starting with a BOM.
func utf16Text(b []byte, bigEndian bool) string {
	var units []uint16
	be := bigEndian
	atStart := true
	for i := 0; i+1 < len(b); i += 2 {
		if atStart && !bigEndian {
			switch {
			case b[i] == 0xFF && b[i+1] == 0xFE:
				be = false
				atStart = false
				continue
			case b[i] == 0xFE && b[i+1] == 0xFF:
				be = true
				atStart = false
				continue
			}
		}
		atStart = false
		var u uint16
		if be {
			u = uint16(b[i])<<8 | uint16(b[i+1])
		} else {
			u = uint16(b[i+1])<<8 | uint16(b[i])
		}
		units = append(units, u)
		if u == 0 {
			atStart = true
		}
	}
	return string(utf16.Decode(units))
}

// id3Genre resolves TCON: numeric references ("17", "(17)") become names,
// several values are joined with " / ".
func id3Genre(v string) string {
	var out []string
	pos := 0
	for pos < len(v) {
		rest := v[pos:]
		if i := strings.IndexByte(rest, 0); i >= 0 {
			out = append(out, parseGenre(rest[:i]))
			pos += i + 1
			continue
		}
		if strings.HasPrefix(rest, "(") && strings.Contains(rest, ")") {
			end := strings.IndexByte(rest, ')')
			s, e := 1, end
			if strings.HasPrefix(rest, "((") {
				e++
			}
			pos += end + 1
			out = append(out, parseGenre(rest[s:min(e, len(rest))]))
			continue
		}
		pos = len(v)
		out = append(out, parseGenre(rest))
	}
	return strings.Join(out, " / ")
}

func parseGenre(g string) string {
	if len(g) > 3 {
		return g
	}
	if id, err := strconv.ParseUint(g, 10, 8); err == nil {
		if int(id) < len(id3v1Genres) {
			return id3v1Genres[id]
		}
		return g
	}
	switch g {
	case "RX":
		return "Remix"
	case "CR":
		return "Cover"
	}
	return g
}

// readID3v1 reads a trailing ID3v1 tag.
func readID3v1(f file) *tagSet {
	if f.size < 128 {
		return nil
	}
	b, ok := f.exact(f.size-128, 128)
	if !ok || string(b[:3]) != "TAG" {
		return nil
	}
	field := func(x []byte) string {
		if i := bytes.IndexByte(x, 0); i >= 0 {
			x = x[:i]
		}
		return strings.TrimSpace(latin1(x))
	}
	t := &tagSet{}
	t.Title = field(b[3:33])
	t.Artist = field(b[33:63])
	t.Album = field(b[63:93])
	t.date = field(b[93:97])
	if int(b[127]) < len(id3v1Genres) {
		t.Genre = id3v1Genres[b[127]]
	}
	return t
}

// readAPE reads a trailing APEv2 tag (before an ID3v1 tag, if any). end
// receives the tag's start, so stream length excludes it.
func readAPE(f file, end int64) (*tagSet, int64) {
	if end < 32 {
		return nil, end
	}
	ft, ok := f.exact(end-32, 32)
	if !ok || string(ft[:8]) != "APETAGEX" {
		return nil, end
	}
	size := int64(binary.LittleEndian.Uint32(ft[12:16])) // items + footer
	count := binary.LittleEndian.Uint32(ft[16:20])
	flags := binary.LittleEndian.Uint32(ft[20:24])
	if size < 32 || size > end || size > maxTagRead {
		return nil, end
	}
	tagStart := end - size
	if flags&0x80000000 != 0 && tagStart >= 32 { // has a header too
		tagStart -= 32
	}
	items := f.at(end-size, int(size-32))
	t := &tagSet{}
	p := 0
	for i := uint32(0); i < count && p+8 <= len(items); i++ {
		vlen := int(binary.LittleEndian.Uint32(items[p:]))
		iflags := binary.LittleEndian.Uint32(items[p+4:])
		p += 8
		z := bytes.IndexByte(items[p:], 0)
		if z < 0 {
			break
		}
		key := strings.ToLower(string(items[p : p+z]))
		p += z + 1
		if vlen < 0 || vlen > len(items)-p {
			break
		}
		val := items[p : p+vlen]
		p += vlen
		if iflags&6 != 0 { // binary or external
			continue
		}
		s := firstValue(lossy(val))
		switch key {
		case "title":
			setIfEmpty(&t.Title, s)
		case "artist":
			setIfEmpty(&t.Artist, s)
		case "album":
			setIfEmpty(&t.Album, s)
		case "genre":
			setIfEmpty(&t.Genre, s)
		case "year":
			setIfEmpty(&t.date, s)
		}
	}
	return t, tagStart
}

// trailingTags: APEv2 (primary for APE, WavPack and Musepack) and ID3v1,
// and where the audio stream ends.
func trailingTags(f file) (ape, id3v1 *tagSet, streamEnd int64) {
	streamEnd = f.size
	id3v1 = readID3v1(f)
	if id3v1 != nil {
		streamEnd -= 128
	}
	ape, streamEnd = readAPE(f, streamEnd)
	return
}

// ---------------------------------------------------------------- Vorbis comments

func vorbisComments(b []byte) *tagSet {
	t := &tagSet{}
	if len(b) < 4 {
		return t
	}
	vlen := int64(binary.LittleEndian.Uint32(b))
	if vlen > int64(len(b)-4) {
		return t
	}
	p := 4 + int(vlen)
	if p+4 > len(b) {
		return t
	}
	n := binary.LittleEndian.Uint32(b[p:])
	p += 4
	var year string
	for i := uint32(0); i < n && p+4 <= len(b); i++ {
		l := int64(binary.LittleEndian.Uint32(b[p:]))
		p += 4
		if l > int64(len(b)-p) {
			break
		}
		c := b[p : p+int(l)]
		p += int(l)
		eq := bytes.IndexByte(c, '=')
		if eq < 0 {
			continue
		}
		key := asciiUpper(string(c[:eq]))
		val := lossy(c[eq+1:])
		switch key {
		case "TITLE":
			setIfEmpty(&t.Title, val)
		case "ARTIST":
			setIfEmpty(&t.Artist, val)
		case "ALBUM":
			setIfEmpty(&t.Album, val)
		case "GENRE":
			setIfEmpty(&t.Genre, val)
		case "DATE":
			setIfEmpty(&t.date, val)
		case "YEAR":
			setIfEmpty(&year, val)
		}
	}
	if t.date == "" {
		t.date = year
	}
	return t
}

// ---------------------------------------------------------------- FLAC

func flacInfo(f file, start int64) *audioInfo {
	info := &audioInfo{container: "flac", codec: "flac", lossless: true}
	pos := start + 4
	var streamInfo []byte
	for range 1024 {
		h, ok := f.exact(pos, 4)
		if !ok {
			return nil
		}
		last := h[0]&0x80 != 0
		typ := h[0] & 0x7f
		l := int64(h[1])<<16 | int64(h[2])<<8 | int64(h[3])
		body := pos + 4
		switch typ {
		case 0:
			b, ok := f.exact(body, int(min(l, 64)))
			if !ok || len(b) < 18 {
				return nil
			}
			streamInfo = b
		case 4:
			if b, ok := f.exact(body, int(min(l, maxComments))); ok {
				if info.primary == nil {
					info.primary = vorbisComments(b)
				}
			}
		}
		pos = body + l
		if last {
			break
		}
	}
	if streamInfo == nil {
		return nil
	}
	x := binary.BigEndian.Uint32(streamInfo[10:14])
	info.sampleRate = x >> 12
	info.channels = uint8((x>>9)&7) + 1
	total := uint64(x&0xF)<<32 | uint64(binary.BigEndian.Uint32(streamInfo[14:18]))
	if info.sampleRate > 0 && total > 0 {
		ms := total * 1000 / uint64(info.sampleRate)
		info.durationMS = ms
		streamLen := uint64(max(f.size-pos, 0))
		if ms > 0 && f.size > 0 && streamLen > 0 {
			info.overallKbps = kbps(uint64(f.size), ms)
			info.audioKbps = kbps(streamLen, ms)
		}
	}
	return info
}

// ---------------------------------------------------------------- Ogg

type oggPage struct {
	headerType byte
	granule    uint64
	serial     uint32
	segments   []byte
	dataAt     int64
	dataLen    int64
	next       int64
}

func readOggPage(f file, pos int64) (oggPage, bool) {
	var p oggPage
	h, ok := f.exact(pos, 27)
	if !ok || string(h[:4]) != "OggS" {
		return p, false
	}
	p.headerType = h[5]
	p.granule = binary.LittleEndian.Uint64(h[6:14])
	p.serial = binary.LittleEndian.Uint32(h[14:18])
	n := int(h[26])
	seg, ok := f.exact(pos+27, n)
	if !ok {
		return p, false
	}
	p.segments = seg
	for _, s := range seg {
		p.dataLen += int64(s)
	}
	p.dataAt = pos + 27 + int64(n)
	p.next = p.dataAt + p.dataLen
	return p, true
}

// oggPackets reads the first n packets of the stream (each capped at
// limit bytes; the rest of a longer packet is dropped).
func oggPackets(f file, pos int64, n int, limit int) ([][]byte, oggPage, bool) {
	var packets [][]byte
	var cur []byte
	var first oggPage
	for pages := 0; len(packets) < n && pages < 4096; pages++ {
		p, ok := readOggPage(f, pos)
		if !ok {
			return packets, first, false
		}
		if pages == 0 {
			first = p
		}
		off := p.dataAt
		for _, s := range p.segments {
			if len(cur) < limit {
				b := f.at(off, int(min(int(s), limit-len(cur))))
				cur = append(cur, b...)
			}
			off += int64(s)
			if s < 255 {
				packets = append(packets, cur)
				cur = nil
				if len(packets) == n {
					break
				}
			}
		}
		pos = p.next
	}
	return packets, first, len(packets) == n
}

// lastGranule is the granule position of the stream's last page.
func lastGranule(f file, serial uint32) (uint64, bool) {
	from := max(f.size-oggTailWindow, 0)
	tail := f.at(from, int(f.size-from))
	for i := len(tail) - 27; i >= 0; i-- {
		if tail[i] != 'O' || string(tail[i:i+4]) != "OggS" {
			continue
		}
		if binary.LittleEndian.Uint32(tail[i+14:]) != serial {
			continue
		}
		g := binary.LittleEndian.Uint64(tail[i+6:])
		if g == math.MaxUint64 {
			continue
		}
		return g, true
	}
	return 0, false
}

func oggInfo(f file, start int64) *audioInfo {
	packets, first, ok := oggPackets(f, start, 2, maxComments)
	if !ok {
		return nil
	}
	id, comments := packets[0], packets[1]
	info := &audioInfo{container: "ogg"}
	switch {
	case bytes.HasPrefix(id, []byte("OpusHead")):
		if len(id) < 19 || !bytes.HasPrefix(comments, []byte("OpusTags")) {
			return nil
		}
		info.codec = "opus"
		info.channels = id[9]
		preSkip := uint64(binary.LittleEndian.Uint16(id[10:]))
		info.sampleRate = binary.LittleEndian.Uint32(id[12:])
		family := id[18]
		if family > 1 || (family == 0 && info.channels > 2) || (family == 1 && info.channels > 8) {
			return nil
		}
		info.primary = vorbisComments(comments[8:])
		if g, ok := lastGranule(f, first.serial); ok {
			total := satSub(satSub(g, first.granule), preSkip)
			if total > 0 {
				ms := divRound(total*1000, 48000)
				info.durationMS = ms
				header := uint64(len(id) + len(comments))
				info.overallKbps = kbps(uint64(f.size), ms)
				info.audioKbps = kbps(satSub(uint64(f.size), header), ms)
			}
		}
	case bytes.HasPrefix(id, []byte("\x01vorbis")):
		if len(id) < 28 || !bytes.HasPrefix(comments, []byte("\x03vorbis")) {
			return nil
		}
		info.codec = "vorbis"
		info.channels = id[11]
		info.sampleRate = binary.LittleEndian.Uint32(id[12:])
		nominal := int32(binary.LittleEndian.Uint32(id[20:]))
		info.primary = vorbisComments(comments[7:])
		if g, ok := lastGranule(f, first.serial); ok && info.sampleRate > 0 {
			total := satSub(g, first.granule)
			if total > 0 {
				ms := total * 1000 / uint64(info.sampleRate)
				info.durationMS = ms
				info.overallKbps = kbps(uint64(f.size), ms)
				if nominal > 0 {
					info.audioKbps = uint32(nominal) / 1000
				} else {
					info.audioKbps = info.overallKbps
				}
			}
		}
	case bytes.HasPrefix(id, []byte("Speex   ")):
		if len(id) < 56 {
			return nil
		}
		info.codec = "speex"
		info.sampleRate = binary.LittleEndian.Uint32(id[36:])
		info.channels = uint8(min(binary.LittleEndian.Uint32(id[48:]), 255))
		br := int32(binary.LittleEndian.Uint32(id[52:]))
		info.primary = vorbisComments(comments)
		if g, ok := lastGranule(f, first.serial); ok && info.sampleRate > 0 {
			total := satSub(g, first.granule)
			if total > 0 {
				ms := total * 1000 / uint64(info.sampleRate)
				info.durationMS = ms
				info.overallKbps = kbps(uint64(f.size), ms)
				if br > 0 {
					info.audioKbps = uint32(br) / 1000
				}
			}
		}
	default:
		return nil
	}
	return info
}

func satSub(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}

// ---------------------------------------------------------------- RIFF / AIFF

func wavInfo(f file, start int64) *audioInfo {
	info := &audioInfo{container: "wav", codec: "pcm", lossless: true}
	pos := start + 12
	var byteRate uint32
	var dataLen int64
	var haveFmt bool
	for range 4096 {
		h, ok := f.exact(pos, 8)
		if !ok {
			break
		}
		id := string(h[:4])
		l := int64(binary.LittleEndian.Uint32(h[4:]))
		body := pos + 8
		switch id {
		case "fmt ":
			b, ok := f.exact(body, 16)
			if !ok {
				return nil
			}
			info.channels = uint8(min(binary.LittleEndian.Uint16(b[2:]), 255))
			info.sampleRate = binary.LittleEndian.Uint32(b[4:])
			byteRate = binary.LittleEndian.Uint32(b[8:])
			haveFmt = true
		case "data":
			dataLen = min(l, f.size-body)
		case "LIST":
			if l >= 4 && l <= maxComments {
				if b, ok := f.exact(body, int(l)); ok && string(b[:4]) == "INFO" && info.other == nil {
					info.other = riffInfo(b[4:])
				}
			}
		case "id3 ", "ID3 ":
			if t, _, ok := readID3v2(f, body); ok && info.primary == nil {
				info.primary = t
			}
		}
		pos = body + l + l&1
	}
	if !haveFmt {
		return nil
	}
	if byteRate > 0 && dataLen > 0 {
		info.durationMS = uint64(dataLen) * 1000 / uint64(byteRate)
		info.audioKbps = byteRate * 8 / 1000
		info.overallKbps = kbps(uint64(f.size), info.durationMS)
	}
	return info
}

func riffInfo(b []byte) *tagSet {
	t := &tagSet{}
	for p := 0; p+8 <= len(b); {
		id := string(b[p : p+4])
		l := int(binary.LittleEndian.Uint32(b[p+4:]))
		p += 8
		if l < 0 || l > len(b)-p {
			break
		}
		v := strings.TrimRight(lossy(b[p:p+l]), "\x00")
		p += l + l&1
		switch id {
		case "INAM":
			t.Title = v
		case "IART":
			t.Artist = v
		case "IPRD":
			t.Album = v
		case "IGNR":
			t.Genre = v
		case "ICRD":
			t.date = v
		}
	}
	return t
}

// extended80 converts an 80-bit IEEE 754 extended float (AIFF sample rate).
func extended80(b []byte) float64 {
	exp := int(binary.BigEndian.Uint16(b[0:2]) & 0x7FFF)
	mant := binary.BigEndian.Uint64(b[2:10])
	if exp == 0 && mant == 0 {
		return 0
	}
	v := math.Ldexp(float64(mant), exp-16383-63)
	if b[0]&0x80 != 0 {
		v = -v
	}
	return v
}

func aiffInfo(f file, start int64) *audioInfo {
	info := &audioInfo{container: "aiff", codec: "pcm", lossless: true}
	pos := start + 12
	var frames uint32
	var bits uint16
	var haveComm bool
	text := &tagSet{}
	for range 4096 {
		h, ok := f.exact(pos, 8)
		if !ok {
			break
		}
		id := string(h[:4])
		l := int64(binary.BigEndian.Uint32(h[4:]))
		body := pos + 8
		switch id {
		case "COMM":
			b, ok := f.exact(body, 18)
			if !ok {
				return nil
			}
			info.channels = uint8(min(binary.BigEndian.Uint16(b[0:]), 255))
			frames = binary.BigEndian.Uint32(b[2:])
			bits = binary.BigEndian.Uint16(b[6:])
			info.sampleRate = f64ToU32(extended80(b[8:18]))
			haveComm = true
		case "ID3 ", "id3 ":
			if t, _, ok := readID3v2(f, body); ok && info.primary == nil {
				info.primary = t
			}
		case "NAME", "AUTH":
			if l <= maxTextFrame {
				if b, ok := f.exact(body, int(l)); ok {
					if id == "NAME" {
						text.Title = lossy(b)
					} else {
						text.Artist = lossy(b)
					}
				}
			}
		}
		pos = body + l + l&1
	}
	if !haveComm {
		return nil
	}
	info.other = text
	if info.sampleRate > 0 && frames > 0 {
		info.durationMS = uint64(frames) * 1000 / uint64(info.sampleRate)
		info.audioKbps = uint32(min(uint64(info.sampleRate)*uint64(bits)*uint64(info.channels)/1000, math.MaxUint32))
		info.overallKbps = kbps(uint64(f.size), info.durationMS)
	}
	return info
}

// ---------------------------------------------------------------- APE, WavPack, Musepack

func apeInfo(f file, start int64) *audioInfo {
	h, ok := f.exact(start, 76)
	if !ok {
		h, ok = f.exact(start, 32)
		if !ok {
			return nil
		}
	}
	info := &audioInfo{container: "ape", codec: "ape", lossless: true}
	version := binary.LittleEndian.Uint16(h[4:])
	var blocksPerFrame, finalBlocks, totalFrames uint32
	if version >= 3980 {
		if len(h) < 8 {
			return nil
		}
		desc := int64(binary.LittleEndian.Uint32(h[8:]))
		hh, ok := f.exact(start+desc, 24)
		if !ok {
			return nil
		}
		blocksPerFrame = binary.LittleEndian.Uint32(hh[4:])
		finalBlocks = binary.LittleEndian.Uint32(hh[8:])
		totalFrames = binary.LittleEndian.Uint32(hh[12:])
		info.channels = uint8(min(binary.LittleEndian.Uint16(hh[18:]), 255))
		info.sampleRate = binary.LittleEndian.Uint32(hh[20:])
	} else {
		compression := binary.LittleEndian.Uint16(h[6:])
		info.channels = uint8(min(binary.LittleEndian.Uint16(h[10:]), 255))
		info.sampleRate = binary.LittleEndian.Uint32(h[12:])
		totalFrames = binary.LittleEndian.Uint32(h[24:])
		finalBlocks = binary.LittleEndian.Uint32(h[28:])
		switch {
		case version >= 3950:
			blocksPerFrame = 73728 * 4
		case version >= 3900 || (version >= 3800 && compression == 4000):
			blocksPerFrame = 73728
		default:
			blocksPerFrame = 9216
		}
	}
	ape, id3v1, end := trailingTags(f)
	info.primary, info.other = ape, id3v1
	if totalFrames > 0 && info.sampleRate > 0 {
		samples := uint64(totalFrames-1)*uint64(blocksPerFrame) + uint64(finalBlocks)
		ms := samples * 1000 / uint64(info.sampleRate)
		info.durationMS = ms
		info.overallKbps = kbps(uint64(f.size), ms)
		info.audioKbps = kbps(uint64(max(end-start, 0)), ms)
	}
	return info
}

var wavpackRates = [15]uint32{6000, 8000, 9600, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000, 64000, 88200, 96000, 192000}

func wavpackInfo(f file, start int64) *audioInfo {
	h, ok := f.exact(start, 32)
	if !ok {
		return nil
	}
	info := &audioInfo{container: "wavpack", codec: "wavpack", lossless: true}
	total := binary.LittleEndian.Uint32(h[12:])
	flags := binary.LittleEndian.Uint32(h[24:])
	if idx := (flags >> 23) & 0xF; idx < 15 {
		info.sampleRate = wavpackRates[idx]
	}
	info.channels = 2
	if flags&4 != 0 {
		info.channels = 1
	}
	ape, id3v1, end := trailingTags(f)
	info.primary, info.other = ape, id3v1
	if total != math.MaxUint32 && total > 0 && info.sampleRate > 0 {
		ms := uint64(total) * 1000 / uint64(info.sampleRate)
		info.durationMS = ms
		info.overallKbps = kbps(uint64(f.size), ms)
		info.audioKbps = kbps(uint64(max(end-start, 0)), ms)
	}
	return info
}

var mpcRates = [4]uint32{44100, 48000, 37800, 32000}

// mpcVarint reads a Musepack SV8 variable-length number.
func mpcVarint(b []byte, p *int) (uint64, bool) {
	var v uint64
	for range 9 {
		if *p >= len(b) {
			return 0, false
		}
		c := b[*p]
		*p++
		v = v<<7 | uint64(c&0x7f)
		if c&0x80 == 0 {
			return v, true
		}
	}
	return 0, false
}

func mpcInfo(f file, start int64) *audioInfo {
	info := &audioInfo{container: "mpc", codec: "musepack"}
	head := f.at(start, 4096)
	var samples uint64
	if bytes.HasPrefix(head, []byte("MP+")) {
		if len(head) < 12 {
			return nil
		}
		frames := binary.LittleEndian.Uint32(head[4:])
		info.sampleRate = mpcRates[(binary.LittleEndian.Uint32(head[8:])>>16)&3]
		info.channels = 2
		samples = uint64(frames) * 1152
	} else {
		// SV8: packets of (key, size, payload); SH is the stream header.
		p := 4
		for p+2 < len(head) {
			key := string(head[p : p+2])
			at := p
			p += 2
			size, ok := mpcVarint(head, &p)
			if !ok || size < uint64(p-at) {
				return nil
			}
			if key == "SH" {
				q := p + 4 // CRC
				if q+1 > len(head) {
					return nil
				}
				q++ // version
				n, ok1 := mpcVarint(head, &q)
				silence, ok2 := mpcVarint(head, &q)
				if !ok1 || !ok2 || q+2 > len(head) {
					return nil
				}
				info.sampleRate = mpcRates[(head[q]>>5)&3]
				info.channels = (head[q+1] >> 4) + 1
				samples = satSub(n, silence)
				break
			}
			p = at + int(min(size, uint64(len(head))))
		}
	}
	ape, id3v1, end := trailingTags(f)
	info.primary, info.other = ape, id3v1
	if samples > 0 && info.sampleRate > 0 {
		ms := samples * 1000 / uint64(info.sampleRate)
		info.durationMS = ms
		info.overallKbps = kbps(uint64(f.size), ms)
		info.audioKbps = kbps(uint64(max(end-start, 0)), ms)
	}
	return info
}

// ---------------------------------------------------------------- MPEG audio / ADTS

type mpegHeader struct {
	version   int // 1, 2, or 25 (MPEG 2.5)
	layer     int
	bitrate   uint32 // kbps
	rate      uint32
	channels  uint8
	mono      bool
	samples   uint32
	length    int
	adts      bool
	adtsSRIdx uint8
}

var mpegBitrates = [2][3][16]uint32{
	{ // MPEG 1: layers I, II, III
		{0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448, 0},
		{0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 0},
		{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0},
	},
	{ // MPEG 2 / 2.5
		{0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256, 0},
		{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
		{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
	},
}

var mpegRates = map[int][3]uint32{1: {44100, 48000, 32000}, 2: {22050, 24000, 16000}, 25: {11025, 12000, 8000}}

var adtsRates = [13]uint32{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}

func parseMPEGHeader(b []byte) (mpegHeader, bool) {
	var h mpegHeader
	if len(b) < 4 || b[0] != 0xFF || b[1]&0xE0 != 0xE0 {
		return h, false
	}
	switch (b[1] >> 3) & 3 {
	case 0:
		h.version = 25
	case 2:
		h.version = 2
	case 3:
		h.version = 1
	default:
		return h, false
	}
	layerBits := (b[1] >> 1) & 3
	if layerBits == 0 {
		return h, false
	}
	h.layer = 4 - int(layerBits)
	bi := b[2] >> 4
	ri := (b[2] >> 2) & 3
	if bi == 0 || bi == 15 || ri == 3 {
		return h, false
	}
	v := 0
	if h.version != 1 {
		v = 1
	}
	h.bitrate = mpegBitrates[v][h.layer-1][bi]
	h.rate = mpegRates[h.version][ri]
	padding := int((b[2] >> 1) & 1)
	h.mono = b[3]>>6 == 3
	h.channels = 2
	if h.mono {
		h.channels = 1
	}
	switch {
	case h.layer == 1:
		h.samples = 384
		h.length = (int(12*h.bitrate*1000/h.rate) + padding) * 4
	case h.layer == 2 || h.version == 1:
		h.samples = 1152
		h.length = int(144*h.bitrate*1000/h.rate) + padding
	default:
		h.samples = 576
		h.length = int(72*h.bitrate*1000/h.rate) + padding
	}
	return h, h.length > 4
}

func parseADTSHeader(b []byte) (mpegHeader, bool) {
	var h mpegHeader
	if len(b) < 7 || b[0] != 0xFF || b[1]&0xF6 != 0xF0 {
		return h, false
	}
	idx := (b[2] >> 2) & 0xF
	if int(idx) >= len(adtsRates) {
		return h, false
	}
	h.adts = true
	h.adtsSRIdx = idx
	h.rate = adtsRates[idx]
	h.channels = (b[2]&1)<<2 | b[3]>>6
	h.length = int(b[3]&3)<<11 | int(b[4])<<3 | int(b[5])>>5
	h.samples = 1024
	return h, h.length >= 7
}

func mpegOrADTS(f file, start int64) *audioInfo {
	buf := f.at(start, syncSearch)
	ape, id3v1, streamEnd := trailingTags(f)
	for i := 0; i+4 <= len(buf); i++ {
		if buf[i] != 0xFF {
			continue
		}
		if h, ok := parseADTSHeader(buf[i:]); ok {
			if next := i + h.length; next+2 > len(buf) || (buf[next] == 0xFF && buf[next+1]&0xF6 == 0xF0) {
				return adtsInfo(f, buf, i, start, streamEnd, id3v1)
			}
		}
		h, ok := parseMPEGHeader(buf[i:])
		if !ok {
			continue
		}
		// The next frame must follow (unless the buffer ends first).
		if next := i + h.length; next+4 <= len(buf) {
			if n, ok := parseMPEGHeader(buf[next:]); !ok || n.version != h.version || n.layer != h.layer {
				continue
			}
		}
		info := &audioInfo{container: "mp3", codec: "mp3", channels: h.channels, sampleRate: h.rate}
		info.other = id3v1
		if info.other == nil {
			info.other = ape
		}
		first := start + int64(i)
		frame := buf[i:min(i+h.length, len(buf))]
		frames, size, vbrOK, isInfo := vbrHeader(frame, h)
		if vbrOK {
			ms := divRound(uint64(h.samples)*1000*uint64(frames), uint64(h.rate))
			if ms > 0 {
				info.durationMS = ms
				info.overallKbps = kbps(uint64(f.size), ms)
				info.audioKbps = kbps(uint64(size), ms)
			}
			return info
		}
		info.audioKbps = h.bitrate
		_ = isInfo
		if streamLen := streamEnd - first; streamLen > 0 && h.bitrate > 0 {
			ms := divRound(uint64(streamLen)*8, uint64(h.bitrate))
			info.durationMS = ms
			info.overallKbps = kbps(uint64(f.size), ms)
		}
		return info
	}
	return nil
}

// vbrHeader reads a Xing/Info or VBRI header in the first frame.
func vbrHeader(frame []byte, h mpegHeader) (frames, size uint32, ok, isInfo bool) {
	off := 4
	switch {
	case h.version == 1 && !h.mono:
		off += 32
	case h.version == 1 || !h.mono:
		off += 17
	default:
		off += 9
	}
	if x, okx := rangeOK(frame, off, 16); okx && (string(x[:4]) == "Xing" || string(x[:4]) == "Info") {
		flags := binary.BigEndian.Uint32(x[4:])
		p := off + 8
		if flags&1 != 0 {
			frames = u32Or(frame, p, 0)
			p += 4
		}
		if flags&2 != 0 {
			size = u32Or(frame, p, 0)
		}
		return frames, size, frames > 0 && size > 0, string(x[:4]) == "Info"
	}
	if v, okv := rangeOK(frame, 36, 18); okv && string(v[:4]) == "VBRI" {
		size = binary.BigEndian.Uint32(v[10:])
		frames = binary.BigEndian.Uint32(v[14:])
		return frames, size, frames > 0 && size > 0, false
	}
	return 0, 0, false, false
}

func adtsInfo(f file, buf []byte, i int, start, streamEnd int64, id3v1 *tagSet) *audioInfo {
	h, _ := parseADTSHeader(buf[i:])
	info := &audioInfo{container: "aac", codec: "aac", channels: h.channels, sampleRate: h.rate, other: id3v1}
	// Average the frame size over the frames in the buffer.
	var n, total uint64
	for p := i; p+7 <= len(buf) && n < 1000; {
		fh, ok := parseADTSHeader(buf[p:])
		if !ok {
			break
		}
		n++
		total += uint64(fh.length)
		p += fh.length
	}
	if n == 0 || h.rate == 0 {
		return info
	}
	// bits per ms = average frame bits × frames per ms
	br := total * 8 * uint64(h.rate) / (n * 1024 * 1000)
	info.audioKbps = uint32(min(br, math.MaxUint32))
	if streamLen := streamEnd - start - int64(i); streamLen > 0 && br > 0 {
		ms := uint64(streamLen) * 8 / br
		info.durationMS = ms
		info.overallKbps = kbps(uint64(f.size), ms)
	}
	return info
}
