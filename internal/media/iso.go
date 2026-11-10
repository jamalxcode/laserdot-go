package media

import (
	"io"
	"iter"
	"math"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ISO base media (MP4, MOV, M4A, 3GP) — header-only (spec §3.1.2).
//
// Top-level boxes are skipped by seeking; only ftyp and moov are read,
// wherever moov sits (front or end of file). Sample data is never read.

// maxMoov: a moov larger than this is treated as corrupt (sample tables of
// very long recordings stay well below it).
const maxMoov = 64 << 20

type fourCC = [4]byte

func cc(s string) fourCC { return fourCC([]byte(s)) }

func parseISO(path string) *Record {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	size := uint64(st.Size())
	return isoFrom(f, size)
}

func isoFrom(f io.ReaderAt, length uint64) *Record {
	var pos uint64
	container := ""
	for pos+8 <= length {
		var h [16]byte
		if _, err := f.ReadAt(h[:8], int64(pos)); err != nil {
			return nil
		}
		size := uint64(u32Or(h[:], 0, 0))
		kind := fourCC(h[4:8])
		header := uint64(8)
		switch size {
		case 1:
			if _, err := f.ReadAt(h[8:16], int64(pos+8)); err != nil {
				return nil
			}
			size = u64Or(h[:], 8, 0)
			header = 16
		case 0:
			size = length - pos
		}
		if size < header || size > length-pos {
			return nil // truncated or corrupt
		}
		bodyLen := size - header
		switch kind {
		case cc("ftyp"):
			var b [4]byte
			if _, err := f.ReadAt(b[:], int64(pos+header)); err != nil {
				return nil
			}
			container = brandContainer(b)
		case cc("moov"):
			if bodyLen > maxMoov {
				return nil
			}
			body := make([]byte, bodyLen)
			if _, err := f.ReadAt(body, int64(pos+header)); err != nil {
				return nil
			}
			rec := moov(body)
			if rec == nil {
				return nil
			}
			if container == "" {
				container = "mov"
			}
			rec.Container = container
			return rec
		}
		pos += size
	}
	return nil
}

func brandContainer(b [4]byte) string {
	switch {
	case b == cc("qt  "):
		return "mov"
	case b == cc("M4A ") || b == cc("M4B ") || b == cc("M4P "):
		return "m4a"
	case b[0] == '3' && b[1] == 'g':
		return "3gp"
	}
	return "mp4"
}

// boxes iterates the child boxes of b: (type, body).
func boxes(b []byte) iter.Seq2[fourCC, []byte] {
	return func(yield func(fourCC, []byte) bool) {
		pos := 0
		for {
			size32, ok := u32At(b, pos)
			if !ok || len(b)-pos < 8 {
				return
			}
			kind := fourCC(b[pos+4 : pos+8])
			var size, header int
			switch size32 {
			case 0:
				size, header = len(b)-pos, 8
			case 1:
				s, ok := u64At(b, pos+8)
				if !ok || s > uint64(math.MaxInt) {
					return
				}
				size, header = int(s), 16
			default:
				if uint64(size32) > uint64(math.MaxInt) {
					return
				}
				size, header = int(size32), 8
			}
			if size < header || size > len(b)-pos {
				return
			}
			body := b[pos+header : pos+size]
			pos += size
			if !yield(kind, body) {
				return
			}
		}
	}
}

func child(b []byte, kind string) ([]byte, bool) {
	want := cc(kind)
	for k, body := range boxes(b) {
		if k == want {
			return body, true
		}
	}
	return nil, false
}

func moov(b []byte) *Record {
	rec := &Record{}
	if mvhd, ok := child(b, "mvhd"); ok {
		v, ok := u8At(mvhd, 0)
		if !ok {
			return nil
		}
		var ts uint32
		var dur uint64
		var ok1, ok2 bool
		if v == 1 {
			ts, ok1 = u32At(mvhd, 20)
			dur, ok2 = u64At(mvhd, 24)
		} else {
			ts, ok1 = u32At(mvhd, 12)
			var d32 uint32
			d32, ok2 = u32At(mvhd, 16)
			dur = uint64(d32)
		}
		if !ok1 || !ok2 {
			return nil
		}
		if ts > 0 && dur != math.MaxUint32 && dur != math.MaxUint64 {
			rec.DurationMS = satMul(dur, 1000) / uint64(ts)
		}
	}
	for kind, body := range boxes(b) {
		switch kind {
		case cc("trak"):
			if t, ok := trak(body, rec); ok {
				rec.Tracks = append(rec.Tracks, t)
			}
		case cc("udta"):
			rec.Tags = udta(body)
		}
	}
	if len(rec.Tracks) == 0 {
		return nil
	}
	return rec
}

func satMul(a, b uint64) uint64 {
	if a != 0 && b > math.MaxUint64/a {
		return math.MaxUint64
	}
	return a * b
}

func abs32(v int32) uint32 {
	if v < 0 {
		return uint32(-int64(v))
	}
	return uint32(v)
}

func trak(b []byte, rec *Record) (Track, bool) {
	var t Track
	mdia, ok := child(b, "mdia")
	if !ok {
		return t, false
	}
	hdlr, ok := child(mdia, "hdlr")
	if !ok {
		return t, false
	}
	h, ok := rangeOK(hdlr, 8, 4)
	if !ok {
		return t, false
	}
	switch string(h) {
	case "vide":
		t.Kind = TrackVideo
	case "soun":
		t.Kind = TrackAudio
	case "sbtl", "subt", "text", "clcp":
		t.Kind = TrackSubtitle
	default:
		return t, false
	}

	// tkhd: enabled flag, display size, rotation matrix
	rotated := false
	var tkW, tkH uint32
	if tkhd, ok := child(b, "tkhd"); ok {
		v, ok := u8At(tkhd, 0)
		if !ok {
			return t, false
		}
		v1 := v == 1
		t.Default = u8Or(tkhd, 3, 0)&1 != 0
		m := 40
		if v1 {
			m = 52
		}
		a := int32(u32Or(tkhd, m, 0x10000))
		bb := int32(u32Or(tkhd, m+4, 0))
		rotated = abs32(bb) == 0x10000 && a == 0
		w := 76
		if v1 {
			w = 88
		}
		tkW, tkH = u32Or(tkhd, w, 0)>>16, u32Or(tkhd, w+4, 0)>>16
	}

	// mdhd: timescale, duration, language
	var timescale uint32
	var mediaDur uint64
	if mdhd, ok := child(mdia, "mdhd"); ok {
		v, ok := u8At(mdhd, 0)
		if !ok {
			return t, false
		}
		v1 := v == 1
		tsAt, langAt := 12, 20
		if v1 {
			tsAt, langAt = 20, 32
		}
		timescale = u32Or(mdhd, tsAt, 0)
		if v1 {
			mediaDur = u64Or(mdhd, 24, 0)
		} else {
			mediaDur = uint64(u32Or(mdhd, 16, 0))
		}
		if code, ok := u16At(mdhd, langAt); ok && code >= 0x400 && code != 0x7FFF {
			chars := []byte{byte((code>>10)&31) + 0x60, byte((code>>5)&31) + 0x60, byte(code&31) + 0x60}
			lower := true
			for _, c := range chars {
				if c < 'a' || c > 'z' {
					lower = false
				}
			}
			if lower {
				t.Lang = NormalizeLang(string(chars))
			}
		}
	}
	if elng, ok := child(mdia, "elng"); ok {
		s := strings.TrimRight(lossy(from(elng, 4)), "\x00")
		if s != "" {
			t.Lang = NormalizeLang(s)
		}
	}

	minf, ok := child(mdia, "minf")
	if !ok {
		return t, false
	}
	stbl, ok := child(minf, "stbl")
	if !ok {
		return t, false
	}
	if stsd, ok := child(stbl, "stsd"); ok {
		if len(stsd) < 8 {
			return t, false
		}
		sampleEntry(stsd[8:], &t)
	}
	if t.Kind == TrackVideo {
		if tkW > 0 && tkH > 0 {
			t.Width, t.Height = tkW, tkH
		}
		if rotated {
			t.Width, t.Height = t.Height, t.Width
		}
		if stts, ok := child(stbl, "stts"); ok {
			fps, vfr := frameTiming(stts, timescale)
			t.FPSMilli = fps
			rec.VFR = rec.VFR || vfr
		}
	}
	if stsz, ok := child(stbl, "stsz"); ok && timescale > 0 && mediaDur > 0 {
		br := satMul(satMul(trackBytes(stsz), 8), uint64(timescale)) / mediaDur
		if br <= math.MaxUint32 {
			t.Bitrate = uint32(br)
		}
	}
	return t, true
}

// lossy is b as text, invalid UTF-8 replaced with U+FFFD.
func lossy(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		sb.WriteRune(r)
		b = b[n:]
	}
	return sb.String()
}

// sampleEntry reads the first sample entry of stsd (after the
// version/count header).
func sampleEntry(e []byte, t *Track) {
	size, ok := u32At(e, 0)
	if !ok {
		return
	}
	if uint64(size) < uint64(len(e)) {
		e = e[:size]
	}
	fcc, ok := rangeOK(e, 4, 4)
	if !ok {
		return
	}
	fourcc := fourCC(fcc)
	switch t.Kind {
	case TrackVideo:
		t.Codec = videoCodec(fourcc)
		t.Width = uint32(u16Or(e, 32, 0))
		t.Height = uint32(u16Or(e, 34, 0))
		t.BitDepth = 8
		switch string(fcc) {
		case "dvh1", "dvhe", "dva1", "dvav":
			t.HDR |= HDRDV
		}
		for k, body := range boxes(from(e, 86)) {
			switch string(k[:]) {
			case "avcC":
				avcc(body).apply(t)
			case "hvcC":
				hvcc(body).apply(t)
			case "av1C":
				av1c(body).apply(t)
			case "vpcC":
				vpcc(body).apply(t)
			case "dvcC", "dvvC", "dvwC":
				t.HDR |= HDRDV
				t.DVProfile = doviProfile(body)
			case "colr":
				if p, ok := rangeOK(body, 0, 4); ok && (string(p) == "nclx" || string(p) == "nclc") {
					switch u16Or(body, 6, 0xFFFF) {
					case 16:
						t.HDR |= HDRHDR10
					case 18:
						t.HDR |= HDRHLG
					}
				}
			}
		}
	case TrackAudio:
		t.Codec = audioCodec(fourcc)
		version := u16Or(e, 16, 0)
		if c, ok := u16At(e, 24); ok {
			t.Channels = uint8(min(c, 255))
		}
		if r, ok := u32At(e, 32); ok {
			t.SampleRate = r >> 16
		}
		childrenAt := 36
		switch version {
		case 1:
			childrenAt = 52
		case 2:
			if bits, ok := u64At(e, 40); ok {
				t.SampleRate = f64ToU32(math.Float64frombits(bits))
			}
			if c, ok := u32At(e, 48); ok {
				t.Channels = uint8(min(c, 255))
			}
			childrenAt = 72
		}
		audioBoxes(from(e, childrenAt), t, 0)
		switch t.Codec {
		case "alac", "flac", "pcm", "truehd", "dts-hd ma":
			t.Lossless = true
		}
	case TrackSubtitle:
		switch string(fcc) {
		case "tx3g", "text":
			t.Codec = "mov_text"
		case "wvtt":
			t.Codec = "webvtt"
		case "stpp":
			t.Codec = "ttml"
		case "c608":
			t.Codec = "eia_608"
		case "c708":
			t.Codec = "eia_708"
		default:
			t.Codec = "unknown"
		}
	}
}

// f64ToU32 converts like Rust's `as`: saturating, NaN → 0.
func f64ToU32(f float64) uint32 {
	switch {
	case f != f || f <= 0:
		return 0
	case f >= math.MaxUint32:
		return math.MaxUint32
	}
	return uint32(f)
}

func audioBoxes(b []byte, t *Track, depth int) {
	for k, body := range boxes(b) {
		switch string(k[:]) {
		case "wave": // QuickTime wrapper
			if depth < 8 {
				audioBoxes(body, t, depth+1)
			}
		case "esds":
			if c := esdsCodec(body); c != "" {
				t.Codec = c
			}
		case "dac3":
			if c, ok := dac3(body); ok {
				t.Channels = c
			}
		case "dec3":
			if c, atmos, ok := dec3(body); ok {
				t.Channels = c
				t.Atmos = atmos
			}
		case "dOps":
			if c, ok := u8At(body, 1); ok {
				t.Channels = c
			}
		}
	}
}

// esdsCodec is the codec from the esds DecoderConfigDescriptor's
// objectTypeIndication ("" when unknown).
func esdsCodec(b []byte) string {
	i := 4 // FullBox header
	readLen := func() bool {
		for range 4 {
			c, ok := u8At(b, i)
			if !ok {
				return false
			}
			i++
			if c&0x80 == 0 {
				break
			}
		}
		return true
	}
	if v, ok := u8At(b, i); !ok || v != 0x03 {
		return ""
	}
	i++
	if !readLen() {
		return ""
	}
	flags, ok := u8At(b, i+2)
	if !ok {
		return ""
	}
	i += 3
	if flags&0x80 != 0 {
		i += 2
	}
	if flags&0x40 != 0 {
		l, ok := u8At(b, i)
		if !ok {
			return ""
		}
		i += 1 + int(l)
	}
	if flags&0x20 != 0 {
		i += 2
	}
	if v, ok := u8At(b, i); !ok || v != 0x04 {
		return ""
	}
	i++
	if !readLen() {
		return ""
	}
	oti, ok := u8At(b, i)
	if !ok {
		return ""
	}
	switch oti {
	case 0x40, 0x66, 0x67, 0x68:
		return "aac"
	case 0x69, 0x6B:
		return "mp3"
	case 0xA5:
		return "ac3"
	case 0xA6:
		return "eac3"
	case 0xA9:
		return "dts"
	case 0xAD:
		return "opus"
	case 0xDD:
		return "vorbis"
	}
	return ""
}

func videoCodec(f fourCC) string {
	switch string(f[:]) {
	case "avc1", "avc3", "dva1", "dvav":
		return "h264"
	case "hvc1", "hev1", "dvh1", "dvhe":
		return "hevc"
	case "av01":
		return "av1"
	case "vp09":
		return "vp9"
	case "vp08":
		return "vp8"
	case "mp4v":
		return "mpeg4"
	case "jpeg", "mjpa", "mjpb":
		return "mjpeg"
	case "apcn", "apch", "apcs", "apco", "ap4h", "ap4x":
		return "prores"
	case "s263", "h263":
		return "h263"
	}
	return "unknown"
}

func audioCodec(f fourCC) string {
	switch string(f[:]) {
	case "mp4a":
		return "aac"
	case "ac-3":
		return "ac3"
	case "ec-3":
		return "eac3"
	case "fLaC":
		return "flac"
	case "alac":
		return "alac"
	case "Opus":
		return "opus"
	case ".mp3":
		return "mp3"
	case "mlpa":
		return "truehd"
	case "dtsc", "dtse":
		return "dts"
	case "dtsh", "dtsl":
		return "dts-hd ma"
	case "lpcm", "sowt", "twos", "in24", "in32", "fl32", "fl64", "raw ", "ipcm":
		return "pcm"
	case "samr":
		return "amr_nb"
	case "sawb":
		return "amr_wb"
	}
	return "unknown"
}

// frameTiming is the frame rate (×1000) from the dominant sample delta, and
// whether the timing is variable (more than one delta beyond a short last
// sample).
func frameTiming(stts []byte, timescale uint32) (uint32, bool) {
	n := int(min(u32Or(stts, 4, 0), 1<<20))
	type entry struct{ count, delta uint32 }
	entries := make([]entry, 0, min(n, 4096))
	for i := range n {
		c, ok1 := u32At(stts, 8+i*8)
		d, ok2 := u32At(stts, 12+i*8)
		if !ok1 || !ok2 {
			break
		}
		entries = append(entries, entry{c, d})
	}
	// The last entry with the largest count (as Rust's max_by_key).
	var dominant, best uint32
	for i, e := range entries {
		if i == 0 || e.count >= best {
			best, dominant = e.count, e.delta
		}
	}
	var fps uint32
	if dominant > 0 && timescale > 0 {
		fps = uint32(min(uint64(timescale)*1000/uint64(dominant), math.MaxUint32))
	}
	distinct := map[uint32]bool{}
	for i, e := range entries {
		if i == len(entries)-1 && e.count == 1 {
			continue
		}
		distinct[e.delta] = true
	}
	return fps, len(distinct) > 1
}

func trackBytes(stsz []byte) uint64 {
	size := u32Or(stsz, 4, 0)
	count := u32Or(stsz, 8, 0)
	if size != 0 {
		return uint64(size) * uint64(count)
	}
	var sum uint64
	for i := 0; i < int(count); i++ {
		v, ok := u32At(stsz, 12+i*4)
		if !ok {
			break
		}
		sum += uint64(v)
	}
	return sum
}

func udta(b []byte) Tags {
	var tags Tags
	for k, body := range boxes(b) {
		switch {
		case k == cc("meta"):
			// MP4 meta is a FullBox; QuickTime's is not.
			inner := from(body, 4)
			if p, ok := rangeOK(body, 4, 4); ok && string(p) == "hdlr" {
				inner = body
			}
			if ilst, ok := child(inner, "ilst"); ok {
				ilstTags(ilst, &tags)
			}
		case k[0] == 0xA9:
			// QuickTime user data: u16 length, u16 language, text
			l := int(u16Or(body, 0, 0))
			if text, ok := rangeOK(body, 4, l); ok {
				setTag(&tags, k, lossy(text))
			}
		}
	}
	return tags
}

func ilstTags(b []byte, tags *Tags) {
	for k, item := range boxes(b) {
		data, ok := child(item, "data")
		if !ok {
			continue
		}
		value := from(data, 8)
		if k == cc("gnre") {
			if idx, ok := u16At(value, 0); ok && idx >= 1 && int(idx) <= len(id3Genres) && tags.Genre == "" {
				tags.Genre = id3Genres[idx-1]
			}
			continue
		}
		setTag(tags, k, lossy(value))
	}
}

func setTag(tags *Tags, k fourCC, v string) {
	v = strings.TrimSpace(strings.TrimRight(v, "\x00"))
	if v == "" {
		return
	}
	switch string(k[:]) {
	case "\xa9nam":
		tags.Title = v
	case "\xa9ART":
		tags.Artist = v
	case "aART":
		if tags.Artist == "" {
			tags.Artist = v
		}
	case "\xa9alb":
		tags.Album = v
	case "\xa9gen":
		tags.Genre = v
	case "\xa9day":
		tags.Year = yearPrefix(v)
	}
}

// yearPrefix: the first four characters as a number (0 when they are not).
func yearPrefix(v string) uint32 {
	if len(v) < 4 {
		return 0
	}
	y, err := strconv.ParseUint(v[:4], 10, 32)
	if err != nil {
		return 0
	}
	return uint32(y)
}

// id3Genres: ID3v1 genre names (the first 80, which cover gnre atoms in
// practice).
var id3Genres = []string{
	"Blues", "Classic Rock", "Country", "Dance", "Disco", "Funk", "Grunge", "Hip-Hop", "Jazz", "Metal",
	"New Age", "Oldies", "Other", "Pop", "R&B", "Rap", "Reggae", "Rock", "Techno", "Industrial",
	"Alternative", "Ska", "Death Metal", "Pranks", "Soundtrack", "Euro-Techno", "Ambient", "Trip-Hop", "Vocal", "Jazz+Funk",
	"Fusion", "Trance", "Classical", "Instrumental", "Acid", "House", "Game", "Sound Clip", "Gospel", "Noise",
	"Alternative Rock", "Bass", "Soul", "Punk", "Space", "Meditative", "Instrumental Pop", "Instrumental Rock", "Ethnic", "Gothic",
	"Darkwave", "Techno-Industrial", "Electronic", "Pop-Folk", "Eurodance", "Dream", "Southern Rock", "Comedy", "Cult", "Gangsta",
	"Top 40", "Christian Rap", "Pop/Funk", "Jungle", "Native American", "Cabaret", "New Wave", "Psychedelic", "Rave", "Showtunes",
	"Trailer", "Lo-Fi", "Tribal", "Acid Punk", "Acid Jazz", "Polka", "Retro", "Musical", "Rock & Roll", "Hard Rock",
}
