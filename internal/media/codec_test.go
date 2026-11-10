package media

import (
	"slices"
	"testing"
)

func TestHEVCMain10Config(t *testing.T) {
	b := make([]byte, 23)
	b[0], b[1] = 1, 0x02
	b[16] = 0xFC | 1
	b[17] = 0xF8 | 2
	c := hvcc(b)
	if c.profile != "main 10" || c.bitDepth != 10 || pixFmt(uint8(c.chroma), c.bitDepth) != "yuv420p10le" {
		t.Fatalf("%+v", c)
	}
}

func TestEAC3AtmosFlag(t *testing.T) {
	// data_rate=640, 1 ind sub, acmod 7 (3/2) + LFE, 0 dep subs, then JOC flag.
	bits := "0000101000000000" + "0110000000001111000" + "00000" + "00000001" + "00010000"
	var b []byte
	for i := 0; i < len(bits); i += 8 {
		chunk := bits[i:min(i+8, len(bits))]
		var v byte
		for _, d := range chunk {
			v = v<<1 | byte(d-'0')
		}
		b = append(b, v<<(8-len(chunk)))
	}
	if c, atmos, ok := dec3(b); !ok || c != 6 || !atmos {
		t.Errorf("full: %d %v %v", c, atmos, ok)
	}
	if c, atmos, ok := dec3(b[:5]); !ok || c != 6 || atmos {
		t.Errorf("short: %d %v %v", c, atmos, ok)
	}
}

func TestAC3_51(t *testing.T) {
	// acmod=7, lfeon=1 → byte1 = 0b00_111_1_00
	if c, ok := dac3([]byte{0x10, 0b0011_1100, 0}); !ok || c != 6 {
		t.Errorf("%d %v", c, ok)
	}
}

func TestTruncatedRecordsDoNotPanic(t *testing.T) {
	for n := range 24 {
		b := make([]byte, n)
		for i := range b {
			b[i] = 0xFF
		}
		_, _, _, _ = avcc(b), hvcc(b), av1c(b), vpcc(b)
		_, _ = dac3(b)
		_, _, _ = dec3(b)
		_ = doviProfile(b)
	}
}

func TestLangNormalizes(t *testing.T) {
	for in, want := range map[string]string{"ara": "ar", "fre": "fr", "en-US": "en", "und": "und"} {
		if got := NormalizeLang(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
	if got := LangAliases("fr"); !slices.Equal(got, []string{"fr", "fra", "fre"}) {
		t.Errorf("aliases: %v", got)
	}
	if LangAliases("xx") != nil {
		t.Error("unknown code has aliases")
	}
}

func TestWantsProbe(t *testing.T) {
	for _, c := range []struct {
		kind Kind
		ext  string
		want bool
	}{
		{Image, "jpg", true}, {Image, "cr3", true}, {Image, "svg", false}, {Video, "mkv", true},
		{Video, "mp4", true}, {Audio, "flac", true}, {Video, "avi", false}, {Audio, "wma", false},
		{Document, "pdf", false},
	} {
		if got := WantsProbe(c.kind, c.ext); got != c.want {
			t.Errorf("%v %s: %v", c.kind, c.ext, got)
		}
	}
	if st, r := Probe("/nonexistent.pdf", Document, "pdf", 0); st != ProbeSkipped || r != nil {
		t.Errorf("pdf: %d", st)
	}
}
