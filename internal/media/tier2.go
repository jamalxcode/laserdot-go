package media

// Tier 2 — deep probing (spec §3.1.1): idle time or on demand, never on the
// indexing path.
//
// Each step runs ffprobe or ffmpeg as a separate process: a crash or a hang
// in a decoder cannot take the indexer down (a hang is killed after a time
// limit). Processes inherit the parent's network sandbox (§7.1), and are
// also told to open local files only (-protocol_whitelist file, paths as
// file: URLs).
//
//   - Tier2Probe: containers Tier 1 does not parse (AVI, MPEG-TS, WMV/ASF,
//     FLV, MPEG-PS…), and files Tier 1 failed on.
//   - HDR10Plus: HDR10+ lives in per-frame SEI, not in headers.
//   - DetectVFR: variable frame rate from packet timestamps, for containers
//     where Tier 1 cannot tell (Matroska, AVI, TS…).
//   - Loudness: EBU R128 loudness, on request only (§3.6).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// probeLimit is the longest a probe step may run before it is killed.
	probeLimit = 30 * time.Second
	// loudnessLimit is the longest a loudness analysis may run (it decodes
	// all of the audio).
	loudnessLimit = 600 * time.Second
)

// Tool finds "ffmpeg" or "ffprobe": $LASERDOT_FFMPEG / $LASERDOT_FFPROBE,
// next to the other one, next to this program (the bundled copy), or a
// standard place. "" when it is not found.
func Tool(name string) string {
	if p, ok := os.LookupEnv("LASERDOT_" + asciiUpper(name)); ok {
		if isFile(p) {
			return p
		}
		return ""
	}
	var candidates []string
	for _, other := range []string{"ffmpeg", "ffprobe"} {
		if other == name {
			continue
		}
		if p, ok := os.LookupEnv("LASERDOT_" + asciiUpper(other)); ok {
			candidates = append(candidates, filepath.Join(filepath.Dir(p), name))
		}
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), name))
	}
	for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"} {
		candidates = append(candidates, filepath.Join(d, name))
	}
	for _, c := range candidates {
		if isFile(c) {
			return c
		}
	}
	return ""
}

// Tier2Available says whether Tier 2 can run here: the ffprobe it would
// use, or "".
func Tier2Available() string { return Tool("ffprobe") }

// input is a local path as an ffmpeg input that cannot be read as another
// protocol.
func input(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return "file:" + abs, nil
}

// runTool runs exe with args, killing it after limit. Stdin is the null
// device; both pipes are drained while it runs. A failure's error is the
// last line of its stderr.
func runTool(exe string, args []string, limit time.Duration) (stdout, stderr []byte, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdin = nil // the null device
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	// A grandchild holding the pipes open must not keep Wait blocked.
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, nil, fmt.Errorf("timed out after %d s", int(limit/time.Second))
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, nil, err
		}
		return nil, nil, errors.New(lastLine(errb.String()))
	}
	return out.Bytes(), errb.Bytes(), nil
}

// lastLine is the last line of s, trimmed ("failed" when s is empty).
func lastLine(s string) string {
	if s == "" {
		return "failed"
	}
	s = strings.TrimSuffix(s, "\n")
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(s, "\r"))
}

func ffprobe(args []string, path string) ([]byte, error) {
	exe := Tool("ffprobe")
	if exe == "" {
		return nil, errors.New("ffprobe not found")
	}
	in, err := input(path)
	if err != nil {
		return nil, err
	}
	all := append([]string{"-v", "error", "-protocol_whitelist", "file"}, args...)
	out, _, err := runTool(exe, append(all, in), probeLimit)
	return out, err
}

// ---------------------------------------------------------------- JSON

// parseJSON decodes ffprobe output keeping numbers exact (json.Number), so
// integer fields can be told from fractional ones as serde_json does.
func parseJSON(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// field is v[key] for an object, else nil.
func field(v any, key string) any {
	if m, ok := v.(map[string]any); ok {
		return m[key]
	}
	return nil
}

func asStr(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func asArr(v any) ([]any, bool) {
	a, ok := v.([]any)
	return a, ok
}

// asU64 is a JSON non-negative integer.
func asU64(v any) (uint64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	u, err := strconv.ParseUint(string(n), 10, 64)
	return u, err == nil
}

// asI64 is a JSON integer.
func asI64(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := strconv.ParseInt(string(n), 10, 64)
	return i, err == nil
}

// jsonNum is a JSON number, or a string holding one.
func jsonNum(v any) (float64, bool) {
	var s string
	switch x := v.(type) {
	case json.Number:
		s = string(x)
	case string:
		s = x
	default:
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

// isOne is v == 1 (an integer).
func isOne(v any) bool {
	i, ok := asI64(v)
	return ok && i == 1
}

// tag is a tag value by case-insensitive key, trimmed; "" when absent or
// empty.
func tag(tags any, key string) string {
	m, ok := tags.(map[string]any)
	if !ok {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic among keys differing only in case
	for _, k := range keys {
		if asciiLower(k) == asciiLower(key) {
			s, _ := asStr(m[k])
			return clip(strings.TrimSpace(s))
		}
	}
	return ""
}

// clip keeps a string within what an encoded record holds (maxString
// bytes, cut at a character boundary).
func clip(s string) string {
	if len(s) <= maxString {
		return s
	}
	i := maxString
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}

// sat converts a non-negative float to an unsigned integer, saturating like
// Rust's `as` (NaN and negatives give 0).
func sat(f float64, max uint64) uint64 {
	switch {
	case !(f > 0):
		return 0
	case f >= float64(max):
		return max
	}
	return uint64(f)
}

// bitrate is a stated bitrate, 0 when absent or beyond 32 bits.
func bitrate(v any) uint32 {
	b, ok := jsonNum(v)
	if !ok {
		return 0
	}
	u := sat(b, math.MaxUint64)
	if u > math.MaxUint32 {
		return 0
	}
	return uint32(u)
}

func ratio(v any) (float64, bool) {
	s, ok := asStr(v)
	if !ok {
		return 0, false
	}
	ns, ds, ok := strings.Cut(s, "/")
	if !ok {
		return 0, false
	}
	n, err1 := strconv.ParseFloat(ns, 64)
	d, err2 := strconv.ParseFloat(ds, 64)
	if err1 != nil || err2 != nil || !(n > 0 && d > 0) {
		return 0, false
	}
	return n / d, true
}

// ---------------------------------------------------------------- probe

// Tier2Probe is a media record from ffprobe's view of the file (streams and
// format).
func Tier2Probe(path string) (*Record, error) {
	out, err := ffprobe([]string{"-of", "json", "-show_format", "-show_streams"}, path)
	if err != nil {
		return nil, err
	}
	root, err := parseJSON(out)
	if err != nil {
		return nil, err
	}
	rec, ok := fromFFprobe(root)
	if !ok {
		return nil, errors.New("no audio or video streams")
	}
	var size uint64
	if st, err := os.Stat(path); err == nil {
		size = uint64(st.Size())
	}
	rec.Finish(size)
	return rec, nil
}

// FromFFprobe builds a record from `ffprobe -show_format -show_streams -of
// json`. False when the JSON is invalid or there is no audio or video
// stream. The record is not finished (see Record.Finish).
func FromFFprobe(j []byte) (*Record, bool) {
	root, err := parseJSON(j)
	if err != nil {
		return nil, false
	}
	return fromFFprobe(root)
}

func fromFFprobe(root any) (*Record, bool) {
	fmtv := field(root, "format")
	rec := &Record{}
	if f, ok := asStr(field(fmtv, "format_name")); ok {
		c, _, _ := strings.Cut(f, ",")
		rec.Container = clip(c)
	}
	if d, ok := jsonNum(field(fmtv, "duration")); ok && d > 0 {
		rec.DurationMS = sat(math.Round(d*1000), math.MaxUint64)
	}
	rec.Bitrate = bitrate(field(fmtv, "bit_rate"))
	tags := field(fmtv, "tags")
	rec.Tags.Title = tag(tags, "title")
	rec.Tags.Artist = tag(tags, "artist")
	if rec.Tags.Artist == "" {
		rec.Tags.Artist = tag(tags, "author")
	}
	rec.Tags.Album = tag(tags, "album")
	rec.Tags.Genre = tag(tags, "genre")
	date := tag(tags, "date")
	if date == "" {
		date = tag(tags, "year")
	}
	if len(date) == 4 || len(date) > 4 && utf8.RuneStart(date[4]) {
		if y, err := strconv.ParseUint(strings.TrimPrefix(date[:4], "+"), 10, 32); err == nil {
			rec.Tags.Year = uint32(y)
		}
	}
	streams, ok := asArr(field(root, "streams"))
	if !ok {
		return nil, false
	}
	for _, s := range streams {
		disp := field(s, "disposition")
		if isOne(field(disp, "attached_pic")) {
			continue // cover art
		}
		var kind TrackKind
		ct, _ := asStr(field(s, "codec_type"))
		switch ct {
		case "video":
			kind = TrackVideo
		case "audio":
			kind = TrackAudio
		case "subtitle":
			kind = TrackSubtitle
		default:
			continue
		}
		codec, _ := asStr(field(s, "codec_name"))
		switch codec {
		case "subrip":
			codec = "srt"
		case "hdmv_pgs_subtitle":
			codec = "pgs"
		case "dvd_subtitle":
			codec = "vobsub"
		}
		var profile string
		if p, _ := asStr(field(s, "profile")); p != "" && p != "unknown" {
			profile = clip(strings.ToLower(p))
		}
		stags := field(s, "tags")
		t := Track{
			Kind:    kind,
			Forced:  isOne(field(disp, "forced")),
			Default: isOne(field(disp, "default")),
			Bitrate: bitrate(field(s, "bit_rate")),
		}
		if l := tag(stags, "language"); l != "" && l != "und" {
			t.Lang = clip(NormalizeLang(l))
		}
		switch kind {
		case TrackVideo:
			w, _ := asU64(field(s, "width"))
			h, _ := asU64(field(s, "height"))
			sideData, _ := asArr(field(s, "side_data_list"))
			var rot int64
			found := false
			for _, d := range sideData {
				if r, ok := asI64(field(d, "rotation")); ok {
					rot, found = r, true
					break
				}
			}
			if !found {
				if r, err := strconv.ParseInt(tag(stags, "rotate"), 10, 64); err == nil {
					rot = r
				}
			}
			t.Width, t.Height = uint32(w), uint32(h)
			if ((rot%180)+180)%180 == 90 {
				t.Width, t.Height = t.Height, t.Width
			}
			fps, ok := ratio(field(s, "avg_frame_rate"))
			if !ok {
				fps, ok = ratio(field(s, "r_frame_rate"))
			}
			if ok {
				t.FPSMilli = uint32(sat(math.Round(fps*1000), math.MaxUint32))
			}
			pix, hasPix := asStr(field(s, "pix_fmt"))
			t.BitDepth = 8
			if b, ok := asStr(field(s, "bits_per_raw_sample")); ok {
				if d, err := strconv.ParseUint(b, 10, 8); err == nil {
					t.BitDepth = uint8(d)
				} else if hasPix {
					t.BitDepth = depthOf(pix)
				}
			} else if hasPix {
				t.BitDepth = depthOf(pix)
			}
			t.PixFmt = clip(pix)
			switch tr, _ := asStr(field(s, "color_transfer")); tr {
			case "smpte2084":
				t.HDR = HDRHDR10
			case "arib-std-b67":
				t.HDR = HDRHLG
			}
			for _, d := range sideData {
				if st, _ := asStr(field(d, "side_data_type")); strings.Contains(st, "DOVI") {
					t.HDR |= HDRDV
					if p, ok := asU64(field(d, "dv_profile")); ok && p <= math.MaxUint8 {
						t.DVProfile = uint8(p)
					}
					break
				}
			}
			t.Profile = profile
		case TrackAudio:
			ch, _ := asU64(field(s, "channels"))
			t.Channels = uint8(min(ch, 255))
			if sr, ok := jsonNum(field(s, "sample_rate")); ok {
				t.SampleRate = uint32(sat(sr, math.MaxUint32))
			}
			t.Atmos = strings.Contains(profile, "atmos") || strings.Contains(profile, "joc")
			switch codec {
			case "flac", "alac", "truehd", "mlp", "wavpack", "ape", "tta":
				t.Lossless = true
			default:
				t.Lossless = strings.HasPrefix(codec, "pcm_") || (codec == "dts" && strings.Contains(profile, "ma"))
			}
			t.Profile = profile
		}
		t.Codec = clip(codec)
		rec.Tracks = append(rec.Tracks, t)
	}
	for _, t := range rec.Tracks {
		if t.Kind == TrackVideo || t.Kind == TrackAudio {
			return rec, true
		}
	}
	return nil, false
}

// depthOf is the bits per channel from a pixel format name (yuv420p10le →
// 10).
func depthOf(pix string) uint8 {
	for _, d := range []uint8{16, 14, 12, 10, 9} {
		if strings.Contains(pix, "p"+strconv.Itoa(int(d))) {
			return d
		}
	}
	return 8
}

// ---------------------------------------------------------------- HDR10+, VFR

// HDR10Plus says whether the first frames of the main video carry HDR10+
// (SMPTE ST 2094-40) dynamic metadata.
func HDR10Plus(path string) (bool, error) {
	out, err := ffprobe([]string{"-select_streams", "v:0", "-read_intervals", "%+#5", "-show_frames",
		"-show_entries", "frame=side_data_list", "-of", "json"}, path)
	if err != nil {
		return false, err
	}
	root, err := parseJSON(out)
	if err != nil {
		return false, err
	}
	return hasHDR10Plus(root), nil
}

// HasHDR10Plus says whether ffprobe -show_frames JSON output mentions HDR10+
// side data.
func HasHDR10Plus(j []byte) bool {
	root, err := parseJSON(j)
	if err != nil {
		return false
	}
	return hasHDR10Plus(root)
}

func hasHDR10Plus(root any) bool {
	frames, _ := asArr(field(root, "frames"))
	for _, f := range frames {
		l, _ := asArr(field(f, "side_data_list"))
		for _, d := range l {
			if t, ok := asStr(field(d, "side_data_type")); ok &&
				(strings.Contains(t, "HDR10+") || strings.Contains(t, "2094-40")) {
				return true
			}
		}
	}
	return false
}

// ErrVFRUnknown: too few video packets to tell whether the frame rate
// varies.
var ErrVFRUnknown = errors.New("too few video frames to tell the frame rate")

// DetectVFR tells variable frame rate from the timestamps of the first 300
// video packets. ErrVFRUnknown when there are too few to tell.
func DetectVFR(path string) (bool, error) {
	out, err := ffprobe([]string{"-select_streams", "v:0", "-read_intervals", "%+#300",
		"-show_entries", "packet=pts_time", "-of", "csv=p=0"}, path)
	if err != nil {
		return false, err
	}
	vfr, ok := IsVFR(ParsePTS(out))
	if !ok {
		return false, ErrVFRUnknown
	}
	return vfr, nil
}

// ParsePTS reads the timestamps of `-show_entries packet=pts_time -of
// csv=p=0`, skipping lines that are not numbers (N/A).
func ParsePTS(out []byte) []float64 {
	var pts []float64
	for _, l := range strings.Split(strings.ToValidUTF8(string(out), "�"), "\n") {
		l = strings.TrimRight(strings.TrimSpace(l), ",")
		if f, err := strconv.ParseFloat(l, 64); err == nil {
			pts = append(pts, f)
		}
	}
	return pts
}

// IsVFR: frame intervals vary — more than 10 % of them differ from the
// median by over 20 %. ok is false with fewer than 10 intervals.
func IsVFR(pts []float64) (vfr, ok bool) {
	pts = append([]float64(nil), pts...)
	sort.Float64s(pts)
	var deltas []float64
	for i := 1; i < len(pts); i++ {
		if pts[i] == pts[i-1] {
			continue
		}
		if d := pts[i] - pts[i-1]; d > 0 {
			deltas = append(deltas, d)
		}
	}
	if len(deltas) < 10 {
		return false, false
	}
	sorted := append([]float64(nil), deltas...)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2]
	off := 0
	for _, d := range deltas {
		if math.Abs((d-median)/median) > 0.2 {
			off++
		}
	}
	return off*10 > len(deltas), true
}

// ---------------------------------------------------------------- loudness

// Loudness is the EBU R128 loudness of the first audio track (spec §3.6,
// story 20).
type Loudness struct {
	// Integrated loudness, LUFS.
	Integrated float64 `json:"integrated"`
	// TruePeak in dBTP.
	TruePeak float64 `json:"true_peak"`
	// Range is the loudness range, LU.
	Range float64 `json:"range"`
}

// GainTo is the gain (dB) that would bring the file to target LUFS.
func (l Loudness) GainTo(target float64) float64 { return target - l.Integrated }

// MeasureLoudness runs ffmpeg's loudnorm analysis over the first audio
// track. (Named so the Loudness type can keep the Rust name.)
func MeasureLoudness(path string) (Loudness, error) {
	exe := Tool("ffmpeg")
	if exe == "" {
		return Loudness{}, errors.New("ffmpeg not found")
	}
	in, err := input(path)
	if err != nil {
		return Loudness{}, err
	}
	_, stderr, err := runTool(exe, []string{"-nostdin", "-hide_banner", "-nostats", "-protocol_whitelist", "file",
		"-i", in, "-vn", "-sn", "-map", "0:a:0", "-af", "loudnorm=print_format=json", "-f", "null", "-"}, loudnessLimit)
	if err != nil {
		return Loudness{}, err
	}
	l, ok := ParseLoudnorm(string(stderr))
	if !ok {
		return Loudness{}, errors.New("no loudness in ffmpeg's output")
	}
	return l, nil
}

// ParseLoudnorm reads the JSON block `loudnorm=print_format=json` prints at
// the end of ffmpeg's stderr.
func ParseLoudnorm(stderr string) (Loudness, bool) {
	start := strings.LastIndexByte(stderr, '{')
	if start < 0 {
		return Loudness{}, false
	}
	end := strings.IndexByte(stderr[start:], '}')
	if end < 0 {
		return Loudness{}, false
	}
	j, err := parseJSON([]byte(stderr[start : start+end+1]))
	if err != nil {
		return Loudness{}, false
	}
	var vals [3]float64
	for i, k := range []string{"input_i", "input_tp", "input_lra"} {
		v, ok := jsonNum(field(j, k))
		if !ok || math.IsInf(v, 0) || math.IsNaN(v) {
			return Loudness{}, false
		}
		vals[i] = v
	}
	return Loudness{Integrated: vals[0], TruePeak: vals[1], Range: vals[2]}, true
}
