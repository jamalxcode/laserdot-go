package media

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Fuzz tests over each parser, without Probe's recover: any input yields a
// record or nil, never a panic or an unbounded allocation.

func seed(f *testing.F, names ...string) {
	for _, n := range names {
		d, err := os.ReadFile(filepath.Join("testdata", n))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(d)
		f.Add(d[:min(len(d), 4096)])
	}
}

func reader(d []byte) file { return file{bytes.NewReader(d), int64(len(d))} }

func FuzzProbeISO(f *testing.F) {
	seed(f, "h264_4k.mp4", "hevc10_pq.mp4", "rotated.mov", "av1.mp4", "song_alac.m4a", "vfr.mp4")
	f.Fuzz(func(t *testing.T, d []byte) {
		if r := isoFrom(bytes.NewReader(d), uint64(len(d))); r != nil {
			r.Finish(uint64(len(d)))
			if _, err := Decode(r.Encode()); err != nil && len(r.Tracks) <= maxTracks {
				t.Fatalf("record does not round-trip: %v", err)
			}
		}
	})
}

func FuzzProbeMatroska(f *testing.F) {
	seed(f, "multi.mkv", "pal.mkv", "vp9.webm")
	f.Fuzz(func(t *testing.T, d []byte) {
		_ = mkvFrom(bytes.NewReader(d), uint64(len(d)))
	})
}

func FuzzProbeAudio(f *testing.F) {
	seed(f, "song.mp3", "song.flac", "voice.opus")
	f.Add([]byte("RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x02\x00\x80\xbb\x00\x00\x00\xee\x02\x00\x04\x00\x10\x00data\x00\x00\x00\x00"))
	f.Fuzz(func(t *testing.T, d []byte) {
		_ = audioFrom(reader(d))
	})
}

func FuzzProbeImage(f *testing.F) {
	seed(f, "base.jpg", "dupes/mandel_small.jpg", "dupes/mandel_turned.jpg")
	f.Fuzz(func(t *testing.T, d []byte) {
		_ = imageFrom(reader(d), "jpg", false)
		_ = imageFrom(reader(d), "cr2", true)
		if ex := findExif(d); ex != nil {
			_ = ex.thumbnail()
		}
		_ = parseTIFF(d)
		_ = heifExif(d)
	})
}
