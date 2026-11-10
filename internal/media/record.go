package media

import (
	"encoding/binary"
	"errors"
	"math"
)

// TrackKind is a track's type.
type TrackKind uint8

const (
	TrackNone TrackKind = iota
	TrackVideo
	TrackAudio
	TrackSubtitle
)

// HDR formats as bit flags (a Dolby Vision profile 8.1 file is also HDR10).
const (
	HDRHDR10 uint8 = 1
	HDRHLG   uint8 = 2
	HDRDV    uint8 = 4
	// HDRHDR10Plus: HDR10+ dynamic metadata (found by Tier 2: it is
	// per-frame, not in headers).
	HDRHDR10Plus uint8 = 8
)

// Track is one stream of a media file (spec §3.2–3.5).
type Track struct {
	Kind TrackKind
	// Codec is the ffprobe-style name: h264, hevc, av1, aac, eac3, truehd, srt…
	Codec   string
	Profile string // "" = unknown
	// Lang: ISO 639-1 where known ("ar"), else the 3-letter code as stored;
	// "" = unknown.
	Lang    string
	Forced  bool
	Default bool
	// video
	Width, Height uint32
	FPSMilli      uint32 // frames per second × 1000
	BitDepth      uint8
	PixFmt        string
	HDR           uint8
	DVProfile     uint8 // 0 = none
	// audio
	Channels   uint8
	SampleRate uint32
	Atmos      bool
	Lossless   bool
	// Bitrate in bits per second when the container states or implies it; 0
	// = unknown.
	Bitrate uint32
}

// Tags are music/video metadata tags.
type Tags struct {
	Title, Artist, Album, Genre string
	Year                        uint32 // 0 = unknown
}

// Exif is camera metadata.
type Exif struct {
	Camera, Lens string
	ISO          uint32  // 0 = unknown
	Aperture     float32 // 0 = unknown
	Focal        float32 // 0 = unknown
	// Taken: capture time as written by the camera (local time read as UTC),
	// Unix seconds; HasTaken says whether it is known.
	Taken      int64
	HasTaken   bool
	GPS        bool
	Screenshot bool
}

// Record is everything Tier 1 learns about a file.
type Record struct {
	// Container: mp4, mov, mkv, webm, mp3, flac, ogg, wav, jpeg, png, heif…
	Container  string
	DurationMS uint64 // 0 = unknown
	Bitrate    uint32 // total bits per second; 0 = unknown
	// Width and Height: display dimensions (rotation applied) of the image
	// or main video track.
	Width, Height uint32
	VFR           bool
	Tracks        []Track
	Tags          Tags
	Exif          *Exif
}

// Video is the first video track, or nil.
func (r *Record) Video() *Track {
	for i := range r.Tracks {
		if r.Tracks[i].Kind == TrackVideo {
			return &r.Tracks[i]
		}
	}
	return nil
}

// TracksOf are the tracks of one kind.
func (r *Record) TracksOf(k TrackKind) []*Track {
	var out []*Track
	for i := range r.Tracks {
		if r.Tracks[i].Kind == k {
			out = append(out, &r.Tracks[i])
		}
	}
	return out
}

// Finish fills derived fields: display size from the first video track, a
// total bitrate from file size and duration when none was stated, and
// drops "und" languages.
func (r *Record) Finish(fileSize uint64) {
	if r.Width == 0 {
		if v := r.Video(); v != nil {
			r.Width, r.Height = v.Width, v.Height
		}
	}
	if r.Bitrate == 0 && r.DurationMS > 0 && fileSize <= math.MaxUint64/8000 {
		if b := fileSize * 8000 / r.DurationMS; b <= math.MaxUint32 {
			r.Bitrate = uint32(b)
		}
	}
	for i := range r.Tracks {
		if r.Tracks[i].Lang == "und" {
			r.Tracks[i].Lang = ""
		}
	}
}

// encodingVersion is the first byte of an encoded record.
const encodingVersion = 1

// Limits that bound decoding of untrusted blobs (a drive's index).
const (
	maxTracks = 256
	maxString = 4096
)

type enc struct{ b []byte }

func (e *enc) u(v uint64)  { e.b = binary.AppendUvarint(e.b, v) }
func (e *enc) i(v int64)   { e.b = binary.AppendVarint(e.b, v) }
func (e *enc) s(v string)  { e.u(uint64(len(v))); e.b = append(e.b, v...) }
func (e *enc) f(v float32) { e.u(uint64(math.Float32bits(v))) }
func (e *enc) bits(vs ...bool) {
	var x uint64
	for i, v := range vs {
		if v {
			x |= 1 << i
		}
	}
	e.u(x)
}

// Encode is a compact binary form for the index.
func (r *Record) Encode() []byte {
	e := &enc{b: []byte{encodingVersion}}
	e.s(r.Container)
	e.u(r.DurationMS)
	e.u(uint64(r.Bitrate))
	e.u(uint64(r.Width))
	e.u(uint64(r.Height))
	e.bits(r.VFR, r.Exif != nil)
	e.u(uint64(len(r.Tracks)))
	for _, t := range r.Tracks {
		e.u(uint64(t.Kind))
		e.s(t.Codec)
		e.s(t.Profile)
		e.s(t.Lang)
		e.bits(t.Forced, t.Default, t.Atmos, t.Lossless)
		e.u(uint64(t.Width))
		e.u(uint64(t.Height))
		e.u(uint64(t.FPSMilli))
		e.u(uint64(t.BitDepth))
		e.s(t.PixFmt)
		e.u(uint64(t.HDR))
		e.u(uint64(t.DVProfile))
		e.u(uint64(t.Channels))
		e.u(uint64(t.SampleRate))
		e.u(uint64(t.Bitrate))
	}
	e.s(r.Tags.Title)
	e.s(r.Tags.Artist)
	e.s(r.Tags.Album)
	e.s(r.Tags.Genre)
	e.u(uint64(r.Tags.Year))
	if x := r.Exif; x != nil {
		e.s(x.Camera)
		e.s(x.Lens)
		e.u(uint64(x.ISO))
		e.f(x.Aperture)
		e.f(x.Focal)
		e.i(x.Taken)
		e.bits(x.HasTaken, x.GPS, x.Screenshot)
	}
	return e.b
}

var errCorrupt = errors.New("corrupt media record")

type dec struct {
	b   []byte
	err error
}

func (d *dec) u(max uint64) uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.b)
	if n <= 0 || v > max {
		d.err = errCorrupt
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *dec) i() int64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Varint(d.b)
	if n <= 0 {
		d.err = errCorrupt
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *dec) s() string {
	n := d.u(maxString)
	if d.err != nil || uint64(len(d.b)) < n {
		d.err = errCorrupt
		return ""
	}
	v := string(d.b[:n])
	d.b = d.b[n:]
	return v
}

func (d *dec) u32() uint32 { return uint32(d.u(math.MaxUint32)) }
func (d *dec) u8() uint8   { return uint8(d.u(math.MaxUint8)) }
func (d *dec) f() float32  { return math.Float32frombits(d.u32()) }
func (d *dec) bits(n int) []bool {
	x := d.u(1<<n - 1)
	out := make([]bool, n)
	for i := range out {
		out[i] = x&(1<<i) != 0
	}
	return out
}

// Decode reads an encoded record. Any malformed input is an error, never a
// panic (drive indexes are untrusted).
func Decode(b []byte) (*Record, error) {
	if len(b) == 0 || b[0] != encodingVersion {
		return nil, errCorrupt
	}
	d := &dec{b: b[1:]}
	r := &Record{}
	r.Container = d.s()
	r.DurationMS = d.u(math.MaxUint64)
	r.Bitrate = d.u32()
	r.Width, r.Height = d.u32(), d.u32()
	fl := d.bits(2)
	r.VFR = fl[0]
	n := d.u(maxTracks)
	for i := uint64(0); i < n && d.err == nil; i++ {
		var t Track
		t.Kind = TrackKind(d.u(uint64(TrackSubtitle)))
		t.Codec, t.Profile, t.Lang = d.s(), d.s(), d.s()
		tb := d.bits(4)
		t.Forced, t.Default, t.Atmos, t.Lossless = tb[0], tb[1], tb[2], tb[3]
		t.Width, t.Height, t.FPSMilli = d.u32(), d.u32(), d.u32()
		t.BitDepth = d.u8()
		t.PixFmt = d.s()
		t.HDR, t.DVProfile, t.Channels = d.u8(), d.u8(), d.u8()
		t.SampleRate, t.Bitrate = d.u32(), d.u32()
		r.Tracks = append(r.Tracks, t)
	}
	r.Tags = Tags{Title: d.s(), Artist: d.s(), Album: d.s(), Genre: d.s(), Year: d.u32()}
	if fl[1] {
		x := &Exif{Camera: d.s(), Lens: d.s(), ISO: d.u32(), Aperture: d.f(), Focal: d.f(), Taken: d.i()}
		xb := d.bits(3)
		x.HasTaken, x.GPS, x.Screenshot = xb[0], xb[1], xb[2]
		r.Exif = x
	}
	if d.err != nil || len(d.b) != 0 {
		return nil, errCorrupt
	}
	return r, nil
}

// ProbeStatus is the outcome of reading a file's header (stored per entry).
type ProbeStatus uint8

const (
	// ProbeSkipped: not a type Tier 1 reads.
	ProbeSkipped ProbeStatus = 0
	// ProbeOK: Record is set.
	ProbeOK ProbeStatus = 1
	// ProbeFailed: header unreadable; the file is still indexed by name
	// (§3.7).
	ProbeFailed ProbeStatus = 2
)
