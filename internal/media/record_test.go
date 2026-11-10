package media

import (
	"reflect"
	"testing"
)

func sample() *Record {
	return &Record{
		Container: "mkv", DurationMS: 5_400_000, Width: 3840, Height: 2160, VFR: true,
		Tracks: []Track{
			{Kind: TrackVideo, Codec: "hevc", Profile: "main 10", Width: 3840, Height: 2160, FPSMilli: 23976, BitDepth: 10, PixFmt: "yuv420p10le", HDR: HDRHDR10 | HDRDV, DVProfile: 8},
			{Kind: TrackAudio, Codec: "truehd", Lang: "ar", Default: true, Channels: 8, SampleRate: 48000, Atmos: true, Lossless: true},
			{Kind: TrackSubtitle, Codec: "subrip", Lang: "en", Forced: true},
		},
		Tags: Tags{Title: "رحلة", Year: 2024},
		Exif: &Exif{Camera: "X", ISO: 100, Aperture: 1.8, Taken: -5, HasTaken: true, GPS: true},
	}
}

func TestRecordRoundTrip(t *testing.T) {
	for _, r := range []*Record{sample(), {}} {
		got, err := Decode(r.Encode())
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Tracks) == 0 {
			got.Tracks = nil
		}
		if !reflect.DeepEqual(got, r) {
			t.Errorf("%+v\n%+v", got, r)
		}
	}
}

func TestFinish(t *testing.T) {
	r := &Record{DurationMS: 2000, Tracks: []Track{{Kind: TrackVideo, Width: 10, Height: 20, Lang: "und"}}}
	r.Finish(1000)
	if r.Width != 10 || r.Height != 20 || r.Bitrate != 4000 || r.Tracks[0].Lang != "" {
		t.Errorf("%+v", r)
	}
}

// Untrusted blobs (a drive's index) never panic and never allocate without
// bound.
func FuzzDecode(f *testing.F) {
	f.Add(sample().Encode())
	f.Add([]byte{1})
	f.Fuzz(func(t *testing.T, b []byte) {
		if r, err := Decode(b); err == nil {
			if _, err := Decode(r.Encode()); err != nil {
				t.Fatal("re-encode failed")
			}
		}
	})
}
