package media

import (
	"encoding/binary"
	"io"
	"iter"
	"math"
	"math/bits"
	"os"
	"slices"
	"strings"
)

// Matroska / WebM — header-only (spec §3.1.2).
//
// Reads the EBML header, then Info, Tracks and Tags. Elements after the
// first Cluster are reached through the SeekHead, so cluster (frame) data is
// never read.

const (
	ebmlID     = 0x1A45_DFA3
	docTypeID  = 0x4282
	segmentID  = 0x1853_8067
	seekHeadID = 0x114D_9B74
	seekID     = 0x4DBB
	seekIDID   = 0x53AB
	seekPosID  = 0x53AC
	infoID     = 0x1549_A966
	tracksID   = 0x1654_AE6B
	tagsID     = 0x1254_C367
	clusterID  = 0x1F43_B675
)

// maxElement: the largest level-1 element read into memory.
const maxElement = 16 << 20

func parseMatroska(path string) *Record {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	return mkvFrom(f, uint64(st.Size()))
}

func satAdd(a, b uint64) uint64 {
	if s := a + b; s >= a {
		return s
	}
	return math.MaxUint64
}

type mkvState struct {
	rec       *Record
	seeks     [][2]uint64 // (id, position)
	timescale uint64
	duration  float64
	hasDur    bool
	title     string
}

func mkvFrom(f io.ReaderAt, length uint64) *Record {
	id, size, known, hlen, ok := ebmlHeader(f, 0)
	if !ok || id != ebmlID || !known {
		return nil
	}
	ebml, ok := readBody(f, hlen, min(size, 4096))
	if !ok {
		return nil
	}
	doctype := ""
	for i, b := range elems(ebml) {
		if i == docTypeID {
			doctype = ebmlString(b)
			break
		}
	}
	pos := satAdd(hlen, size)

	id, size, known, hlen, ok = ebmlHeader(f, pos)
	if !ok || id != segmentID {
		return nil
	}
	segStart := pos + hlen
	segEnd := length
	if known {
		segEnd = min(satAdd(segStart, size), length)
	}

	container := "mkv"
	if doctype == "webm" {
		container = "webm"
	}
	s := &mkvState{rec: &Record{Container: container}, timescale: 1_000_000}
	var seen []uint32

	pos = segStart
	for pos < segEnd {
		id, size, known, hlen, ok := ebmlHeader(f, pos)
		if !ok || !known || id == clusterID { // unknown-size element: stop
			break
		}
		switch id {
		case seekHeadID, infoID, tracksID, tagsID:
			if size <= maxElement {
				b, ok := readBody(f, pos+hlen, size)
				if !ok {
					return nil
				}
				s.level1(id, b)
				seen = append(seen, id)
			}
		}
		pos = satAdd(pos+hlen, size)
	}
	// Info/Tracks/Tags placed after the clusters: follow the SeekHead.
	for _, sk := range slices.Clone(s.seeks) {
		id := uint32(sk[0])
		if slices.Contains(seen, id) || (id != infoID && id != tracksID && id != tagsID) {
			continue
		}
		at := satAdd(segStart, sk[1])
		found, size, known, hlen, ok := ebmlHeader(f, at)
		if ok && known && found == id && size <= maxElement && satAdd(at+hlen, size) <= length {
			if b, ok := readBody(f, at+hlen, size); ok {
				s.level1(id, b)
				seen = append(seen, id)
			}
		}
	}
	rec := s.rec
	if !slices.Contains(seen, tracksID) || len(rec.Tracks) == 0 {
		return nil
	}
	if s.hasDur && !math.IsInf(s.duration, 0) && !math.IsNaN(s.duration) && s.duration > 0 {
		rec.DurationMS = f64ToU64(s.duration * float64(s.timescale) / 1e6)
	}
	if rec.Tags.Title == "" {
		rec.Tags.Title = s.title
	}
	return rec
}

// f64ToU64 converts like Rust's `as`: saturating, NaN → 0.
func f64ToU64(f float64) uint64 {
	switch {
	case f != f || f <= 0:
		return 0
	case f >= math.MaxUint64:
		return math.MaxUint64
	}
	return uint64(f)
}

func (s *mkvState) level1(id uint32, b []byte) {
	switch id {
	case seekHeadID:
		for i, seek := range elems(b) {
			if i != seekID {
				continue
			}
			var sid, spos uint64
			var hasID, hasPos bool
			for j, v := range elems(seek) {
				switch j {
				case seekIDID:
					sid, hasID = uint64(uint32(ebmlUint(v))), true
				case seekPosID:
					spos, hasPos = ebmlUint(v), true
				}
			}
			if hasID && hasPos && len(s.seeks) < 1024 {
				s.seeks = append(s.seeks, [2]uint64{sid, spos})
			}
		}
	case infoID:
		for i, v := range elems(b) {
			switch i {
			case 0x2A_D7B1:
				s.timescale = max(ebmlUint(v), 1)
			case 0x4489:
				s.duration, s.hasDur = ebmlFloat(v)
			case 0x7BA9:
				s.title = ebmlString(v)
			}
		}
	case tracksID:
		for i, entry := range elems(b) {
			if i == 0xAE {
				if t, ok := mkvTrack(entry); ok {
					s.rec.Tracks = append(s.rec.Tracks, t)
				}
			}
		}
	case tagsID:
		s.rec.Tags = mkvTags(b)
	}
}

func mkvTrack(b []byte) (Track, bool) {
	t := Track{Default: true}
	var codecID string
	var private []byte
	var langLegacy, langBCP47 string
	var hasLegacy, hasBCP47 bool
	var defaultDuration uint64
	var colourDepth uint8
	var subH, subV uint64
	var hasSubH, hasSubV bool
	for i, v := range elems(b) {
		switch i {
		case 0x83:
			switch ebmlUint(v) {
			case 1:
				t.Kind = TrackVideo
			case 2:
				t.Kind = TrackAudio
			case 17:
				t.Kind = TrackSubtitle
			default:
				t.Kind = TrackNone
			}
		case 0x86:
			codecID = ebmlString(v)
		case 0x63A2:
			private = v
		case 0x22_B59C:
			langLegacy, hasLegacy = ebmlString(v), true
		case 0x22_B59D:
			langBCP47, hasBCP47 = ebmlString(v), true
		case 0x55AA:
			t.Forced = ebmlUint(v) == 1
		case 0x88:
			t.Default = ebmlUint(v) == 1
		case 0x23_E383:
			defaultDuration = ebmlUint(v)
		case 0xE0:
			for j, w := range elems(v) {
				switch j {
				case 0xB0:
					t.Width = uint32(ebmlUint(w))
				case 0xBA:
					t.Height = uint32(ebmlUint(w))
				case 0x55B0:
					for k, c := range elems(w) {
						switch k {
						case 0x55B2:
							colourDepth = uint8(ebmlUint(c))
						case 0x55B3:
							subH, hasSubH = ebmlUint(c), true
						case 0x55B4:
							subV, hasSubV = ebmlUint(c), true
						case 0x55BA:
							switch ebmlUint(c) {
							case 16:
								t.HDR |= HDRHDR10
							case 18:
								t.HDR |= HDRHLG
							}
						}
					}
				}
			}
		case 0xE1:
			for j, w := range elems(v) {
				switch j {
				case 0xB5:
					fl, _ := ebmlFloat(w)
					t.SampleRate = f64ToU32(fl)
				case 0x9F:
					t.Channels = uint8(min(ebmlUint(w), 255))
				}
			}
		case 0x41E4:
			var kind uint64
			var extra []byte
			for j, w := range elems(v) {
				switch j {
				case 0x41E7:
					kind = ebmlUint(w)
				case 0x41ED:
					extra = w
				}
			}
			if kind == uint64(binary.BigEndian.Uint32([]byte("dvcC"))) ||
				kind == uint64(binary.BigEndian.Uint32([]byte("dvvC"))) {
				t.HDR |= HDRDV
				t.DVProfile = doviProfile(extra)
			}
		}
	}
	if t.Kind == TrackNone {
		return t, false
	}
	// Matroska's default language is English when the element is absent.
	l := "eng"
	if hasBCP47 {
		l = langBCP47
	} else if hasLegacy {
		l = langLegacy
	}
	t.Lang = NormalizeLang(l)
	t.Codec = mkvCodecName(codecID)
	switch t.Kind {
	case TrackVideo:
		t.BitDepth = 8
		switch t.Codec {
		case "h264":
			avcc(private).apply(&t)
		case "hevc":
			hvcc(private).apply(&t)
		case "av1":
			av1c(private).apply(&t)
		}
		if t.PixFmt == "" && colourDepth > 0 {
			t.BitDepth = colourDepth
			if hasSubH && hasSubV {
				chroma := uint8(3)
				switch {
				case subH == 1 && subV == 1:
					chroma = 1
				case subH == 1 && subV == 0:
					chroma = 2
				}
				t.PixFmt = pixFmt(chroma, colourDepth)
			}
		}
		if defaultDuration > 0 {
			t.FPSMilli = uint32(min(1_000_000_000_000/defaultDuration, math.MaxUint32))
		}
	case TrackAudio:
		switch t.Codec {
		case "flac", "truehd", "pcm", "alac", "wavpack":
			t.Lossless = true
		}
	}
	return t, true
}

func mkvCodecName(id string) string {
	switch id {
	case "V_MPEG4/ISO/AVC":
		return "h264"
	case "V_MPEGH/ISO/HEVC":
		return "hevc"
	case "V_AV1":
		return "av1"
	case "V_VP9":
		return "vp9"
	case "V_VP8":
		return "vp8"
	case "V_MPEG2", "V_MPEG1":
		return "mpeg2video"
	case "V_MPEG4/ISO/ASP", "V_MPEG4/ISO/SP", "V_MPEG4/ISO/AP":
		return "mpeg4"
	case "V_PRORES":
		return "prores"
	case "V_THEORA":
		return "theora"
	case "V_MJPEG":
		return "mjpeg"
	case "V_FFV1":
		return "ffv1"
	case "A_AC3":
		return "ac3"
	case "A_EAC3":
		return "eac3"
	case "A_DTS":
		return "dts"
	case "A_TRUEHD", "A_MLP":
		return "truehd"
	case "A_FLAC":
		return "flac"
	case "A_OPUS":
		return "opus"
	case "A_VORBIS":
		return "vorbis"
	case "A_ALAC":
		return "alac"
	case "A_WAVPACK4":
		return "wavpack"
	case "A_MPEG/L3":
		return "mp3"
	case "A_MPEG/L2":
		return "mp2"
	case "S_TEXT/UTF8", "S_TEXT/ASCII":
		return "srt"
	case "S_TEXT/ASS", "S_TEXT/SSA", "S_ASS", "S_SSA":
		return "ass"
	case "S_TEXT/WEBVTT", "D_WEBVTT/SUBTITLES", "D_WEBVTT/CAPTIONS":
		return "webvtt"
	case "S_HDMV/PGS":
		return "pgs"
	case "S_VOBSUB":
		return "vobsub"
	case "S_DVBSUB":
		return "dvbsub"
	}
	switch {
	case strings.HasPrefix(id, "A_AAC"):
		return "aac"
	case strings.HasPrefix(id, "A_PCM"):
		return "pcm"
	}
	return "unknown"
}

func mkvTags(b []byte) Tags {
	var out Tags
	for i, tag := range elems(b) {
		if i != 0x7373 {
			continue
		}
		global := true
		for j, targets := range elems(tag) {
			if j != 0x63C0 {
				continue
			}
			for k, v := range elems(targets) {
				if k == 0x63C5 && ebmlUint(v) != 0 {
					global = false
				}
			}
		}
		if !global {
			continue
		}
		for j, simple := range elems(tag) {
			if j != 0x67C8 {
				continue
			}
			var name, value string
			for k, v := range elems(simple) {
				switch k {
				case 0x45A3:
					name = asciiUpper(ebmlString(v))
				case 0x4487:
					value = ebmlString(v)
				}
			}
			if value == "" {
				continue
			}
			switch name {
			case "TITLE":
				out.Title = value
			case "ARTIST", "ALBUM_ARTIST":
				if out.Artist == "" {
					out.Artist = value
				}
			case "ALBUM":
				out.Album = value
			case "GENRE":
				out.Genre = value
			case "DATE_RELEASED", "DATE_RECORDED", "DATE", "YEAR":
				out.Year = yearPrefix(value)
			}
		}
	}
	return out
}

// ebmlHeader reads an element header at pos: id, size (known == false when
// the size is the "unknown" marker), header length.
func ebmlHeader(f io.ReaderAt, pos uint64) (id uint32, size uint64, known bool, hlen uint64, ok bool) {
	if pos > math.MaxInt64 {
		return
	}
	var buf [12]byte
	n, err := f.ReadAt(buf[:], int64(pos))
	if n == 0 && err != nil {
		return
	}
	b := buf[:n]
	v, ilen, ok1 := vint(b, true)
	if !ok1 {
		return
	}
	size, slen, ok2 := vint(b[ilen:], false)
	if !ok2 {
		return
	}
	unknown := size == (uint64(1)<<(7*slen))-1
	return uint32(v), size, !unknown, uint64(ilen + slen), true
}

func readBody(f io.ReaderAt, pos, size uint64) ([]byte, bool) {
	if pos > math.MaxInt64 || size > math.MaxInt32 {
		return nil, false
	}
	b := make([]byte, size)
	if _, err := f.ReadAt(b, int64(pos)); err != nil && !(err == io.EOF && size == 0) {
		return nil, false
	}
	return b, true
}

// vint reads an EBML variable-length integer. IDs keep their marker bit;
// sizes drop it.
func vint(b []byte, keepMarker bool) (uint64, int, bool) {
	if len(b) == 0 {
		return 0, 0, false
	}
	first := b[0]
	l := bits.LeadingZeros8(first) + 1
	if l > 8 || (keepMarker && l > 4) {
		return 0, 0, false
	}
	v := uint64(first)
	if !keepMarker {
		v &= (1 << (8 - l)) - 1
	}
	for i := 1; i < l; i++ {
		if i >= len(b) {
			return 0, 0, false
		}
		v = v<<8 | uint64(b[i])
	}
	return v, l, true
}

// elems iterates the child elements of an in-memory element body.
func elems(b []byte) iter.Seq2[uint32, []byte] {
	return func(yield func(uint32, []byte) bool) {
		pos := 0
		for pos <= len(b) {
			rest := b[pos:]
			id, ilen, ok := vint(rest, true)
			if !ok {
				return
			}
			size, slen, ok := vint(rest[ilen:], false)
			if !ok {
				return
			}
			start := ilen + slen
			if size > uint64(len(rest)-start) {
				return
			}
			end := start + int(size)
			pos += end
			if !yield(uint32(id), rest[start:end]) {
				return
			}
		}
	}
}

func ebmlUint(b []byte) uint64 {
	var v uint64
	for _, x := range b[:min(len(b), 8)] {
		v = v<<8 | uint64(x)
	}
	return v
}

func ebmlFloat(b []byte) (float64, bool) {
	switch len(b) {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b))), true
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(b)), true
	}
	return 0, false
}

func ebmlString(b []byte) string {
	return strings.TrimRight(lossy(b), "\x00")
}
