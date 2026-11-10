package media

// Tier 2 (spec §3.1.1) on its corpus (testdata/tier2): containers Tier 1
// does not read, VFR in Matroska, loudness. Tests that run ffprobe/ffmpeg
// skip where they are not installed; the parsing of their output is also
// tested on canned outputs (testdata/tier2/canned.sh), which always runs.

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fx2(name string) string { return filepath.Join("testdata", name) }

func needFFmpeg(t *testing.T) {
	t.Helper()
	if Tier2Available() == "" || Tool("ffmpeg") == "" {
		t.Skip("ffprobe/ffmpeg not installed — Tier 2 tests skipped")
	}
}

// probed is a record from the real ffprobe.
func probed(t *testing.T) func(string) *Record {
	return func(name string) *Record {
		t.Helper()
		r, err := Tier2Probe(fx2(name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return r
	}
}

// canned is a record from committed ffprobe output, finished with the
// fixture's size as Tier2Probe does.
func canned(t *testing.T) func(string) *Record {
	return func(name string) *Record {
		t.Helper()
		j, err := os.ReadFile(filepath.Join("testdata/tier2/ffprobe", filepath.Base(name)+".json"))
		if err != nil {
			t.Fatal(err)
		}
		r, ok := FromFFprobe(j)
		if !ok {
			t.Fatalf("%s: no record", name)
		}
		st, err := os.Stat(fx2(name))
		if err != nil {
			t.Fatal(err)
		}
		r.Finish(uint64(st.Size()))
		return r
	}
}

func TestTier1DoesNotReadTheseContainers(t *testing.T) {
	for _, c := range [][2]string{{"tier2/legacy.avi", "avi"}, {"tier2/broadcast.ts", "ts"}, {"tier2/clip.wmv", "wmv"}, {"tier2/old.flv", "flv"}, {"tier2/dvd.mpg", "mpg"}} {
		if st, _ := Probe(fx2(c[0]), Video, c[1], 1); st != ProbeSkipped {
			t.Errorf("%s: status %d", c[0], st)
		}
	}
}

func checkLegacyContainers(t *testing.T, get func(string) *Record) {
	r := get("tier2/legacy.avi")
	eq(t, "avi container", r.Container, "avi")
	v := r.Video()
	eq(t, "avi video", sp(v.Codec, v.Width, v.Height, v.FPSMilli), sp("mpeg4", 320, 240, 25_000))
	eq(t, "avi profile", v.Profile, "simple profile")
	a := firstOf(t, r, TrackAudio)
	eq(t, "avi audio", sp(a.Codec, a.Channels, a.SampleRate), sp("mp3", 2, 48_000))
	if r.DurationMS < 1000 || r.DurationMS >= 1100 {
		t.Errorf("avi duration %d", r.DurationMS)
	}

	r = get("tier2/broadcast.ts")
	eq(t, "ts container", r.Container, "mpegts")
	eq(t, "ts video", r.Video().Codec, "h264")
	audio := r.TracksOf(TrackAudio)
	if len(audio) != 2 {
		t.Fatalf("ts audio tracks: %d", len(audio))
	}
	eq(t, "ts audio 0", sp(audio[0].Codec, audio[0].Lang, audio[0].Channels), sp("aac", "ar", 2))
	eq(t, "ts audio 1", sp(audio[1].Codec, audio[1].Lang, audio[1].Channels), sp("ac3", "en", 6))

	r = get("tier2/clip.wmv")
	eq(t, "wmv container", r.Container, "asf")
	eq(t, "wmv video", sp(r.Video().Codec, r.Width, r.Height), sp("wmv2", 640, 360))

	r = get("tier2/old.flv")
	eq(t, "flv video", r.Video().Codec, "flv1")
	eq(t, "flv channels", firstOf(t, r, TrackAudio).Channels, 1)

	r = get("tier2/dvd.mpg")
	eq(t, "mpg container", r.Container, "mpeg")
	v = r.Video()
	eq(t, "mpg video", sp(v.Codec, v.Width, v.Height), sp("mpeg2video", 720, 576))
}

func TestLegacyContainers(t *testing.T) {
	needFFmpeg(t)
	checkLegacyContainers(t, probed(t))
}

func TestLegacyContainersCanned(t *testing.T) {
	checkLegacyContainers(t, canned(t))
}

// The Tier 1 corpus seen by Tier 2 agrees with Tier 1 on the essentials.
func checkAgreesWithTier1(t *testing.T, get func(string) *Record) {
	r := get("hevc10_pq.mp4")
	v := r.Video()
	eq(t, "hevc", sp(v.Codec, v.BitDepth, v.HDR), sp("hevc", 10, HDRHDR10))
	eq(t, "hevc lang", firstOf(t, r, TrackAudio).Lang, "ar")
	r = get("multi.mkv")
	eq(t, "mkv audio tracks", len(r.TracksOf(TrackAudio)), 3)
	eq(t, "mkv subtitle", firstOf(t, r, TrackSubtitle).Codec, "srt")
}

func TestAgreesWithTier1(t *testing.T) {
	needFFmpeg(t)
	checkAgreesWithTier1(t, probed(t))
	if r, err := Tier2Probe(fx2("base.jpg")); err == nil && r.Video() == nil {
		t.Error("base.jpg: a record without video")
	}
	if _, err := Tier2Probe(fx2("truncated.mp4")); err == nil {
		t.Error("truncated.mp4 probed")
	}
}

func TestAgreesWithTier1Canned(t *testing.T) {
	checkAgreesWithTier1(t, canned(t))
	// Each canned record agrees with Tier 1 on the essentials.
	for _, n := range []string{"hevc10_pq.mp4", "multi.mkv", "h264_4k.mp4", "rotated.mov", "song.flac"} {
		r2, r1 := canned(t)(n), rec(t, n)
		eq(t, n+" size", sp(r2.Width, r2.Height), sp(r1.Width, r1.Height))
		eq(t, n+" tracks", len(r2.Tracks), len(r1.Tracks))
		for i := range min(len(r1.Tracks), len(r2.Tracks)) {
			a, b := r2.Tracks[i], r1.Tracks[i]
			eq(t, n+" track", sp(a.Kind, a.Codec, a.Lang, a.Channels, a.SampleRate, a.FPSMilli),
				sp(b.Kind, b.Codec, b.Lang, b.Channels, b.SampleRate, b.FPSMilli))
		}
		if d := int64(r2.DurationMS) - int64(r1.DurationMS); d < -100 || d > 100 {
			t.Errorf("%s duration %d vs %d", n, r2.DurationMS, r1.DurationMS)
		}
	}
	if _, ok := FromFFprobe([]byte(`{"streams": [{"codec_type": "subtitle"}]}`)); ok {
		t.Error("a record without audio or video")
	}
	if _, ok := FromFFprobe([]byte(`not json`)); ok {
		t.Error("a record from junk")
	}
}

func TestFromFFprobeFields(t *testing.T) {
	j := `{"format": {"format_name": "mov,mp4,m4a", "duration": "2.5", "bit_rate": "64000",
	        "tags": {"TITLE": " Trip ", "author": "Me", "date": "2019-05-01"}},
	  "streams": [
	    {"codec_type": "video", "codec_name": "mjpeg", "disposition": {"attached_pic": 1}},
	    {"codec_type": "video", "codec_name": "hevc", "profile": "Main 10", "width": 1920, "height": 1080,
	     "avg_frame_rate": "0/0", "r_frame_rate": "24000/1001", "pix_fmt": "yuv420p10le",
	     "color_transfer": "arib-std-b67",
	     "side_data_list": [{"side_data_type": "Display Matrix", "rotation": -90},
	                        {"side_data_type": "DOVI configuration record", "dv_profile": 8}]},
	    {"codec_type": "audio", "codec_name": "eac3", "profile": "Dolby Digital Plus + Dolby Atmos",
	     "channels": 6, "sample_rate": "48000", "bit_rate": "768000",
	     "tags": {"language": "ara"}, "disposition": {"default": 1}},
	    {"codec_type": "audio", "codec_name": "dts", "profile": "DTS-HD MA", "channels": 8,
	     "tags": {"language": "und"}},
	    {"codec_type": "subtitle", "codec_name": "hdmv_pgs_subtitle", "disposition": {"forced": 1}},
	    {"codec_type": "data", "codec_name": "bin_data"}
	  ]}`
	r, ok := FromFFprobe([]byte(j))
	if !ok {
		t.Fatal("no record")
	}
	eq(t, "format", sp(r.Container, r.DurationMS, r.Bitrate), sp("mov", 2500, 64000))
	eq(t, "tags", sp(r.Tags.Title, r.Tags.Artist, r.Tags.Year), sp("Trip", "Me", 2019))
	eq(t, "tracks", len(r.Tracks), 4)
	v := r.Tracks[0]
	eq(t, "video", sp(v.Codec, v.Profile, v.Width, v.Height, v.FPSMilli, v.BitDepth, v.HDR, v.DVProfile),
		sp("hevc", "main 10", 1080, 1920, 23976, 10, HDRHLG|HDRDV, 8))
	a := r.Tracks[1]
	eq(t, "eac3", sp(a.Lang, a.Default, a.Atmos, a.Lossless, a.Bitrate, a.SampleRate), sp("ar", true, true, false, 768000, 48000))
	a = r.Tracks[2]
	eq(t, "dts", sp(a.Lang, a.Lossless, a.Channels), sp("", true, 8))
	s := r.Tracks[3]
	eq(t, "pgs", sp(s.Codec, s.Forced), sp("pgs", true))
}

func TestVariableFrameRateFromPackets(t *testing.T) {
	needFFmpeg(t)
	for _, c := range []struct {
		name string
		want bool
	}{{"tier2/vfr.mkv", true}, {"pal.mkv", false}, {"h264_4k.mp4", false}} {
		got, err := DetectVFR(fx2(c.name))
		if err != nil || got != c.want {
			t.Errorf("%s: %v, %v", c.name, got, err)
		}
	}
}

func TestVariableFrameRateCanned(t *testing.T) {
	for _, c := range []struct {
		name string
		want bool
	}{{"vfr.mkv", true}, {"pal.mkv", false}, {"h264_4k.mp4", false}} {
		b, err := os.ReadFile(filepath.Join("testdata/tier2/pts", c.name+".csv"))
		if err != nil {
			t.Fatal(err)
		}
		got, ok := IsVFR(ParsePTS(b))
		if !ok || got != c.want {
			t.Errorf("%s: %v, %v", c.name, got, ok)
		}
	}
}

func TestVFRFromIntervals(t *testing.T) {
	var cfr, vfr []float64
	for i := range 50 {
		cfr = append(cfr, float64(i)/25)
		d := 0.0
		if i%3 == 0 {
			d = 0.02
		}
		vfr = append(vfr, float64(i)/25+d)
	}
	if got, ok := IsVFR(cfr); !ok || got {
		t.Errorf("cfr: %v %v", got, ok)
	}
	if got, ok := IsVFR(vfr); !ok || !got {
		t.Errorf("vfr: %v %v", got, ok)
	}
	if _, ok := IsVFR([]float64{0, 0.04}); ok {
		t.Error("two timestamps decided")
	}
	if got := ParsePTS([]byte("0.000000,\nN/A\n 0.040000\n")); len(got) != 2 || got[1] != 0.04 {
		t.Errorf("ParsePTS: %v", got)
	}
}

func TestHDR10PlusSideDataIsRecognised(t *testing.T) {
	yes := `{"frames": [{"side_data_list": [
		{"side_data_type": "Mastering display metadata"},
		{"side_data_type": "HDR Dynamic Metadata SMPTE2094-40 (HDR10+)", "application_version": 1}
	]}]}`
	no := `{"frames": [{"side_data_list": [{"side_data_type": "Content light level metadata"}]}]}`
	if !HasHDR10Plus([]byte(yes)) || HasHDR10Plus([]byte(no)) || HasHDR10Plus([]byte(`{}`)) {
		t.Error("HDR10+ side data misread")
	}
	b, err := os.ReadFile("testdata/tier2/frames/hevc10_pq.mp4.json")
	if err != nil {
		t.Fatal(err)
	}
	if HasHDR10Plus(b) {
		t.Error("hevc10_pq.mp4 has no HDR10+")
	}
}

func TestHDR10PlusProbe(t *testing.T) {
	needFFmpeg(t)
	if got, err := HDR10Plus(fx2("hevc10_pq.mp4")); err != nil || got {
		t.Errorf("hevc10_pq.mp4: %v, %v", got, err)
	}
	if _, err := HDR10Plus(fx2("tier2/nope.mkv")); err == nil {
		t.Error("missing file probed")
	}
}

func checkLoudness(t *testing.T, loud, quiet Loudness) {
	// Half the amplitude is 6 dB quieter.
	if math.Abs(loud.Integrated-quiet.Integrated-6) >= 0.5 {
		t.Errorf("%+v vs %+v", loud, quiet)
	}
	// A steady sine has (almost) no loudness range, and its true peak sits
	// within a few dB of its integrated loudness.
	if loud.Range >= 1 || math.Abs(loud.TruePeak-loud.Integrated) >= 4 {
		t.Errorf("%+v", loud)
	}
}

func TestLoudnessOnDemand(t *testing.T) {
	needFFmpeg(t)
	loud, err := MeasureLoudness(fx2("tier2/tone.flac"))
	if err != nil {
		t.Fatal(err)
	}
	quiet, err := MeasureLoudness(fx2("tier2/tone_quiet.flac"))
	if err != nil {
		t.Fatal(err)
	}
	checkLoudness(t, loud, quiet)
	if _, err := MeasureLoudness(fx2("tier2/nope.flac")); err == nil {
		t.Error("missing file measured")
	}
}

func TestLoudnessCanned(t *testing.T) {
	read := func(n string) Loudness {
		b, err := os.ReadFile(filepath.Join("testdata/tier2/loudnorm", n+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		l, ok := ParseLoudnorm(string(b))
		if !ok {
			t.Fatalf("%s: no loudness", n)
		}
		return l
	}
	checkLoudness(t, read("tone"), read("tone_quiet"))
}

func TestLoudnormOutput(t *testing.T) {
	stderr := "[Parsed_loudnorm_0 @ 0x1]\n{\n\t\"input_i\" : \"-23.04\",\n\t\"input_tp\" : \"-20.00\",\n\t\"input_lra\" : \"0.00\",\n\t\"input_thresh\" : \"-33.04\"\n}\n"
	l, ok := ParseLoudnorm(stderr)
	if !ok || l != (Loudness{Integrated: -23.04, TruePeak: -20, Range: 0}) {
		t.Fatalf("%+v %v", l, ok)
	}
	if math.Abs(l.GainTo(-14)-9.04) >= 1e-9 {
		t.Error(l.GainTo(-14))
	}
	if _, ok := ParseLoudnorm(`{"input_i": "-inf"}`); ok {
		t.Error("-inf accepted")
	}
}

func TestPixelDepths(t *testing.T) {
	eq(t, "10", depthOf("yuv420p10le"), 10)
	eq(t, "8", depthOf("yuv420p"), 8)
	eq(t, "p010le", depthOf("p010le"), 8) // not a pNN depth suffix
}

func TestToolLookup(t *testing.T) {
	dir := t.TempDir()
	probe := filepath.Join(dir, "ffprobe")
	if err := os.WriteFile(probe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	// Set and valid: used. Set and missing: nothing, no fallback.
	t.Setenv("LASERDOT_FFPROBE", probe)
	eq(t, "env", Tool("ffprobe"), probe)
	t.Setenv("LASERDOT_FFPROBE", filepath.Join(dir, "nope"))
	eq(t, "env missing", Tool("ffprobe"), "")
	// ffmpeg next to $LASERDOT_FFPROBE.
	t.Setenv("LASERDOT_FFPROBE", probe)
	ffmpeg := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(ffmpeg, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, set := os.LookupEnv("LASERDOT_FFMPEG"); !set {
		eq(t, "sibling", Tool("ffmpeg"), ffmpeg)
		eq(t, "dupe finder", ffmpegPath(), ffmpeg)
	}
}

func TestRunToolErrors(t *testing.T) {
	eq(t, "last line", lastLine("a\nfile: No such file or directory \n"), "file: No such file or directory")
	eq(t, "empty", lastLine(""), "failed")
	sh := "/bin/sh"
	if _, err := os.Stat(sh); err != nil {
		t.Skip("no /bin/sh")
	}
	if _, _, err := runTool(sh, []string{"-c", "echo one >&2; echo two >&2; exit 3"}, probeLimit); err == nil || err.Error() != "two" {
		t.Errorf("exit error: %v", err)
	}
	if _, _, err := runTool(sh, []string{"-c", "sleep 5"}, 200*time.Millisecond); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("timeout: %v", err)
	}
	out, _, err := runTool(sh, []string{"-c", "cat; echo done"}, probeLimit)
	if err != nil || string(out) != "done\n" {
		t.Errorf("stdin is not null: %q %v", out, err)
	}
}

// Any ffprobe output yields a record that round-trips, or nothing; never a
// panic.
func FuzzFromFFprobe(f *testing.F) {
	ms, _ := filepath.Glob("testdata/tier2/ffprobe/*.json")
	for _, m := range ms {
		if b, err := os.ReadFile(m); err == nil {
			f.Add(b)
		}
	}
	f.Fuzz(func(t *testing.T, j []byte) {
		if r, ok := FromFFprobe(j); ok {
			r.Finish(uint64(len(j)))
			if _, err := Decode(r.Encode()); err != nil && len(r.Tracks) <= maxTracks {
				t.Fatalf("record does not round-trip: %v", err)
			}
		}
		_ = HasHDR10Plus(j)
		_, _ = ParseLoudnorm(string(j))
		_, _ = IsVFR(ParsePTS(j))
	})
}
