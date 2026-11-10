package media

// Seam 2 (spec §15.1): the golden corpus through the real Tier 1 pipeline,
// asserting on the parsed schema. Fixtures come from testdata/generate.sh
// (the Rust corpus, unchanged).

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func fixture(name string) string { return filepath.Join("testdata", name) }

func run(t testing.TB, path string) (ProbeStatus, *Record) {
	t.Helper()
	name := strings.ToLower(filepath.Base(path))
	ext := Extension(name)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	size := uint64(st.Size())
	return Probe(path, Classify(ext, false, size), ext, size)
}

func rec(t *testing.T, name string) *Record {
	t.Helper()
	st, r := run(t, fixture(name))
	if st != ProbeOK || r == nil {
		t.Fatalf("%s: status %d", name, st)
	}
	return r
}

func firstOf(t *testing.T, r *Record, k TrackKind) *Track {
	t.Helper()
	ts := r.TracksOf(k)
	if len(ts) == 0 {
		t.Fatalf("no track of kind %d", k)
	}
	return ts[0]
}

func eq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func TestH264_4kMP4(t *testing.T) {
	r := rec(t, "h264_4k.mp4")
	eq(t, "container", r.Container, "mp4")
	eq(t, "size", [2]uint32{r.Width, r.Height}, [2]uint32{3840, 2160})
	eq(t, "duration", r.DurationMS, 1000)
	v := r.Video()
	eq(t, "codec", v.Codec, "h264")
	eq(t, "fps", v.FPSMilli, 24_000)
	eq(t, "pix_fmt", v.PixFmt, "yuv420p")
	a := firstOf(t, r, TrackAudio)
	eq(t, "audio", sp(a.Codec, a.Channels, a.SampleRate), sp("aac", 2, 48_000))
	eq(t, "lang", a.Lang, "en")
	eq(t, "title", r.Tags.Title, "Big Trip")
}

func TestHEVCMain10HDR10EAC3(t *testing.T) {
	r := rec(t, "hevc10_pq.mp4")
	v := r.Video()
	eq(t, "codec", v.Codec, "hevc")
	eq(t, "profile", v.Profile, "main 10")
	eq(t, "depth", v.BitDepth, 10)
	eq(t, "pix_fmt", v.PixFmt, "yuv420p10le")
	eq(t, "hdr", v.HDR, HDRHDR10)
	eq(t, "fps", v.FPSMilli, 29_970)
	a := firstOf(t, r, TrackAudio)
	eq(t, "codec", a.Codec, "eac3")
	eq(t, "channels", a.Channels, 6)
	eq(t, "lang", a.Lang, "ar")
}

func TestRotationGivesDisplaySizeAndMoovAtEndIsFound(t *testing.T) {
	r := rec(t, "rotated.mov")
	eq(t, "container", r.Container, "mov")
	eq(t, "size", [2]uint32{r.Width, r.Height}, [2]uint32{1080, 1920})
}

func TestAV1AndVP9(t *testing.T) {
	r := rec(t, "av1.mp4")
	eq(t, "codec", r.Video().Codec, "av1")
	eq(t, "profile", r.Video().Profile, "main")
	r = rec(t, "vp9.webm")
	eq(t, "container", r.Container, "webm")
	eq(t, "codec", r.Video().Codec, "vp9")
	eq(t, "fps", r.Video().FPSMilli, 60_000)
	eq(t, "audio", firstOf(t, r, TrackAudio).Codec, "opus")
}

func TestMatroskaTracksLanguagesSubtitlesTags(t *testing.T) {
	r := rec(t, "multi.mkv")
	eq(t, "container", r.Container, "mkv")
	v := r.Video()
	eq(t, "video", sp(v.Width, v.Height, v.FPSMilli, v.BitDepth), sp(3840, 1600, 23_976, 10))
	eq(t, "hdr", v.HDR, HDRHLG)
	var audio []string
	for _, a := range r.TracksOf(TrackAudio) {
		audio = append(audio, sp(a.Codec, a.Channels, a.Lang, a.Lossless))
	}
	eq(t, "audio", strings.Join(audio, "|"), "ac3 6 ar false|aac 2 en false|flac 8 fr true")
	var subs []string
	for _, s := range r.TracksOf(TrackSubtitle) {
		subs = append(subs, sp(s.Codec, s.Lang, s.Forced))
	}
	eq(t, "subs", strings.Join(subs, "|"), "srt en true|srt ar false")
	eq(t, "title", r.Tags.Title, "Desert Concert")
	eq(t, "artist", r.Tags.Artist, "Fairouz")
	if r.DurationMS < 1000 || r.DurationMS >= 1100 {
		t.Errorf("duration %d", r.DurationMS)
	}
}

func TestPALAndVFR(t *testing.T) {
	r := rec(t, "pal.mkv")
	eq(t, "codec", r.Video().Codec, "mpeg2video")
	eq(t, "fps", r.Video().FPSMilli, 25_000)
	eq(t, "pal vfr", r.VFR, false)
	eq(t, "vfr.mp4 vfr", rec(t, "vfr.mp4").VFR, true)
	eq(t, "h264 vfr", rec(t, "h264_4k.mp4").VFR, false)
}

func TestMusicTagsAcrossFormats(t *testing.T) {
	for _, f := range []string{"song_alac.m4a", "song.flac", "song.mp3"} {
		r := rec(t, f)
		eq(t, f+" artist", r.Tags.Artist, "Fairouz")
		eq(t, f+" album", r.Tags.Album, "Live at Baalbek")
		eq(t, f+" genre", r.Tags.Genre, "Classical")
		eq(t, f+" title", r.Tags.Title, "Ya Rayah")
		eq(t, f+" year", r.Tags.Year, 1970)
		if r.DurationMS < 950 || r.DurationMS >= 1100 {
			t.Errorf("%s: duration %d", f, r.DurationMS)
		}
	}
	eq(t, "alac lossless", rec(t, "song_alac.m4a").Tracks[0].Lossless, true)
	eq(t, "flac lossless", rec(t, "song.flac").Tracks[0].Lossless, true)
	eq(t, "mp3 lossless", rec(t, "song.mp3").Tracks[0].Lossless, false)
	o := rec(t, "voice.opus")
	eq(t, "opus codec", o.Tracks[0].Codec, "opus")
	eq(t, "opus genre", o.Tags.Genre, "Electronic")
}

func TestTruncatedFileFailsCleanly(t *testing.T) {
	st, r := run(t, fixture("truncated.mp4"))
	if st != ProbeFailed || r != nil {
		t.Fatalf("status %d, record %v", st, r)
	}
}

func TestRecordRoundtripsThroughStorageEncoding(t *testing.T) {
	for _, f := range []string{"multi.mkv", "hevc10_pq.mp4", "song.flac"} {
		r := rec(t, f)
		got, err := Decode(r.Encode())
		if err != nil || !reflect.DeepEqual(got, r) {
			t.Errorf("%s: round trip: %v\n got %+v\nwant %+v", f, err, got, r)
		}
		if n := len(r.Encode()); n >= 400 {
			t.Errorf("%s: %d bytes", f, n)
		}
	}
}

// mutations: garbage, truncations and bit flips of data (40 variants, as
// the Rust test).
func mutations(data []byte) [][]byte {
	var out [][]byte
	seed := uint64(0x2545_F491_4F6C_DD1D)
	for round := range 40 {
		d := bytes.Clone(data)
		switch round % 4 {
		case 0:
			d = d[:len(d)*round/40]
		case 1:
			for range 8 {
				seed ^= seed << 13
				seed ^= seed >> 7
				seed ^= seed << 17
				i := int(seed % uint64(max(len(d), 1)))
				if i < len(d) {
					d[i] ^= 1 << (seed % 8)
				}
			}
		case 2:
			for i := range min(64, len(d)) {
				d[i] = 0xFF
			}
		default:
			d = d[:min(64, len(d))]
		}
		out = append(out, d)
	}
	return out
}

// TestCorruptInputsNeverPanic: garbage, truncations and bit flips of every
// fixture, through the parsers directly (Probe would also recover).
func TestCorruptInputsNeverPanic(t *testing.T) {
	dir := t.TempDir()
	ents, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		ext := strings.TrimPrefix(filepath.Ext(e.Name()), ".")
		if e.IsDir() || ext == "" || ext == "sh" {
			continue
		}
		data, err := os.ReadFile(fixture(e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for round, d := range mutations(data) {
			p := filepath.Join(dir, fmt.Sprintf("x%d.%s", round, ext))
			if err := os.WriteFile(p, d, 0o644); err != nil {
				t.Fatal(err)
			}
			_, _ = run(t, p)
			parseAll(d, ext)
		}
	}
}

// parseAll runs every parser on in-memory data without Probe's recover, so
// a panic fails the test.
func parseAll(d []byte, ext string) {
	f := file{bytes.NewReader(d), int64(len(d))}
	_ = isoFrom(f.r, uint64(len(d)))
	_ = mkvFrom(f.r, uint64(len(d)))
	_ = audioFrom(f)
	_ = imageFrom(f, ext, false)
	_ = imageFrom(f, ext, true)
}

// tiffWriter builds a big-endian TIFF block with IFD0 and Exif/GPS
// sub-IFDs (the Rust test used kamadak-exif's writer).
type tiffEntry struct {
	tag, typ uint16
	count    uint32
	data     []byte
}

func ascii(tag uint16, s string) tiffEntry {
	return tiffEntry{tag, 2, uint32(len(s) + 1), append([]byte(s), 0)}
}

func short(tag uint16, v uint16) tiffEntry {
	return tiffEntry{tag, 3, 1, binary.BigEndian.AppendUint16(nil, v)}
}

func rationals(tag uint16, vs ...[2]uint32) tiffEntry {
	var b []byte
	for _, v := range vs {
		b = binary.BigEndian.AppendUint32(b, v[0])
		b = binary.BigEndian.AppendUint32(b, v[1])
	}
	return tiffEntry{tag, 5, uint32(len(vs)), b}
}

func writeTIFF(ifd0, exif, gps []tiffEntry) []byte {
	out := []byte("MM\x00*\x00\x00\x00\x08")
	// Layout: IFD0 at 8, then Exif IFD, then GPS IFD, then value data.
	ifdLen := func(n int) int { return 2 + 12*n + 4 }
	ifd0 = append(ifd0, tiffEntry{0x8769, 4, 1, nil}, tiffEntry{0x8825, 4, 1, nil})
	exifAt := 8 + ifdLen(len(ifd0))
	gpsAt := exifAt + ifdLen(len(exif))
	dataAt := gpsAt + ifdLen(len(gps))
	var data []byte
	write := func(es []tiffEntry) {
		out = binary.BigEndian.AppendUint16(out, uint16(len(es)))
		for _, e := range es {
			out = binary.BigEndian.AppendUint16(out, e.tag)
			out = binary.BigEndian.AppendUint16(out, e.typ)
			out = binary.BigEndian.AppendUint32(out, e.count)
			v := e.data
			switch e.tag {
			case 0x8769:
				v = binary.BigEndian.AppendUint32(nil, uint32(exifAt))
			case 0x8825:
				v = binary.BigEndian.AppendUint32(nil, uint32(gpsAt))
			}
			if len(v) <= 4 {
				out = append(out, append(v, make([]byte, 4-len(v))...)...)
			} else {
				out = binary.BigEndian.AppendUint32(out, uint32(dataAt+len(data)))
				data = append(data, v...)
				if len(data)%2 == 1 {
					data = append(data, 0)
				}
			}
		}
		out = binary.BigEndian.AppendUint32(out, 0)
	}
	write(ifd0)
	write(exif)
	write(gps)
	return append(out, data...)
}

func TestJPEGExifAndOrientation(t *testing.T) {
	tiff := writeTIFF(
		[]tiffEntry{ascii(0x10F, "Canon"), ascii(0x110, "Canon EOS R5"), short(0x112, 6)},
		[]tiffEntry{
			ascii(0xA434, "RF24-70mm F2.8 L IS USM"),
			short(0x8827, 3200),
			rationals(0x829D, [2]uint32{28, 10}),
			rationals(0x920A, [2]uint32{50, 1}),
			ascii(0x9003, "2024:03:09 15:30:00"),
		},
		[]tiffEntry{rationals(0x0002, [2]uint32{25, 1}, [2]uint32{25, 1}, [2]uint32{25, 1})},
	)
	// JPEG = SOI + APP1("Exif\0\0" + TIFF) + the rest of a real JPEG
	base, err := os.ReadFile(fixture("base.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE1}
	jpeg = binary.BigEndian.AppendUint16(jpeg, uint16(len(tiff)+8))
	jpeg = append(jpeg, "Exif\x00\x00"...)
	jpeg = append(jpeg, tiff...)
	jpeg = append(jpeg, base[2:]...)
	path := filepath.Join(t.TempDir(), "IMG_0001.jpg")
	if err := os.WriteFile(path, jpeg, 0o644); err != nil {
		t.Fatal(err)
	}
	st, r := run(t, path)
	if st != ProbeOK {
		t.Fatalf("status %d", st)
	}
	eq(t, "orientation 6 swaps to display size", [2]uint32{r.Width, r.Height}, [2]uint32{48, 64})
	e := r.Exif
	if e == nil {
		t.Fatal("no exif")
	}
	eq(t, "camera", e.Camera, "Canon EOS R5")
	eq(t, "lens", e.Lens, "RF24-70mm F2.8 L IS USM")
	eq(t, "iso", e.ISO, 3200)
	eq(t, "aperture", e.Aperture, 2.8)
	eq(t, "focal", e.Focal, 50.0)
	eq(t, "taken", e.Taken, 1_709_998_200)
	eq(t, "has taken", e.HasTaken, true)
	eq(t, "gps", e.GPS, true)
}

func TestExifTime(t *testing.T) {
	if v, ok := exifTime("2024:03:09 15:30:00"); !ok || v != 1_709_998_200 {
		t.Errorf("got %d %v", v, ok)
	}
	if _, ok := exifTime("0000:00:00 00:00:00"); ok {
		t.Error("zero date accepted")
	}
}

// TestMatchesFFprobe: field-by-field check against ffprobe (spec §15.1).
// Runs when ffprobe is installed; skipped otherwise.
func TestMatchesFFprobe(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed — skipping cross-check")
	}
	for _, name := range []string{
		"h264_4k.mp4", "hevc10_pq.mp4", "rotated.mov", "av1.mp4", "vp9.webm", "multi.mkv",
		"vfr.mp4", "pal.mkv", "song_alac.m4a", "song.flac", "song.mp3", "voice.opus",
	} {
		out, err := exec.Command("ffprobe", "-v", "error", "-of", "json", "-show_streams", "-show_format",
			fixture(name)).Output()
		if err != nil {
			t.Fatalf("%s: ffprobe: %v", name, err)
		}
		var j struct {
			Streams []map[string]any `json:"streams"`
			Format  struct {
				Duration string `json:"duration"`
			} `json:"format"`
		}
		if err := json.Unmarshal(out, &j); err != nil {
			t.Fatal(err)
		}
		r := rec(t, name)
		var streams []map[string]any
		for _, s := range j.Streams {
			disp, _ := s["disposition"].(map[string]any)
			if s["codec_type"] != "attachment" && num(disp["attached_pic"]) != 1 {
				streams = append(streams, s)
			}
		}
		if len(streams) != len(r.Tracks) {
			t.Errorf("%s: track count %d vs %d", name, len(r.Tracks), len(streams))
			continue
		}
		for i, s := range streams {
			tr := r.Tracks[i]
			ctx := fmt.Sprintf("%s stream %v", name, s["index"])
			codec, _ := s["codec_name"].(string)
			if codec == "subrip" {
				codec = "srt"
			}
			eq(t, ctx+": codec", tr.Codec, codec)
			tags, _ := s["tags"].(map[string]any)
			if lang, _ := tags["language"].(string); lang != "" && lang != "und" {
				eq(t, ctx+": lang", tr.Lang, NormalizeLang(lang))
			}
			switch s["codec_type"] {
			case "video":
				w, h := uint32(num(s["width"])), uint32(num(s["height"]))
				var rot float64
				if l, ok := s["side_data_list"].([]any); ok {
					for _, d := range l {
						if m, ok := d.(map[string]any); ok {
							if v, ok := m["rotation"]; ok {
								rot = num(v)
								break
							}
						}
					}
				}
				if math.Abs(rot) == 90 || math.Abs(rot) == 270 {
					w, h = h, w
				}
				eq(t, ctx+": display size", [2]uint32{tr.Width, tr.Height}, [2]uint32{w, h})
				nd := strings.SplitN(s["r_frame_rate"].(string), "/", 2)
				n, _ := strconv.ParseFloat(nd[0], 64)
				d, _ := strconv.ParseFloat(nd[1], 64)
				if fps := n / d; math.Abs(float64(tr.FPSMilli)/1000-fps) >= 0.01 {
					t.Errorf("%s: fps %d vs %f", ctx, tr.FPSMilli, fps)
				}
				if tr.PixFmt != "" {
					eq(t, ctx+": pix_fmt", tr.PixFmt, s["pix_fmt"].(string))
				}
				if tr.Profile != "" {
					p, _ := s["profile"].(string)
					eq(t, ctx+": profile", tr.Profile, strings.ToLower(p))
				}
				var hdr uint8
				switch s["color_transfer"] {
				case "smpte2084":
					hdr = HDRHDR10
				case "arib-std-b67":
					hdr = HDRHLG
				}
				eq(t, ctx+": hdr", tr.HDR, hdr)
			case "audio":
				eq(t, ctx+": channels", uint64(tr.Channels), uint64(num(s["channels"])))
				sr, _ := strconv.ParseUint(s["sample_rate"].(string), 10, 32)
				eq(t, ctx+": sample rate", tr.SampleRate, uint32(sr))
			case "subtitle":
				disp, _ := s["disposition"].(map[string]any)
				eq(t, ctx+": forced", tr.Forced, num(disp["forced"]) == 1)
			}
		}
		fdur, _ := strconv.ParseFloat(j.Format.Duration, 64)
		ours := float64(r.DurationMS) / 1000
		// ffprobe applies edit lists (start offsets); we report the container
		// duration. They differ by a few frames on edited files.
		if math.Abs(ours-fdur) >= 0.1 {
			t.Errorf("%s: duration %f vs %f", name, ours, fdur)
		}
	}
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

// sp joins values with spaces.
func sp(vs ...any) string { return strings.TrimSuffix(fmt.Sprintln(vs...), "\n") }
