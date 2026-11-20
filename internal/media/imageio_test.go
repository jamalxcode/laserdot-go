package media

import "testing"

// Every 8-bit pixel layout CoreGraphics hands back gives the same luma.
func TestLumaFromEveryPixelLayout(t *testing.T) {
	const r, g, b, a = 200, 100, 50, 255
	want := byte((2126*uint32(r) + 7152*uint32(g) + 722*uint32(b)) / 10000)
	cases := []struct {
		name string
		px   []byte
		bpp  int
		info uint32
	}{
		{"RGBA big", []byte{r, g, b, a}, 32, 1},
		{"ARGB big", []byte{a, r, g, b}, 32, 2},
		{"RGBX big", []byte{r, g, b, 0}, 32, 5},
		{"XRGB big", []byte{0, r, g, b}, 32, 6},
		{"ARGB little (BGRA in memory)", []byte{b, g, r, a}, 32, 2 | cgByteOrder32Little},
		{"RGBA little (ABGR in memory)", []byte{a, b, g, r}, 32, 1 | cgByteOrder32Little},
		{"RGB", []byte{r, g, b}, 24, 0},
	}
	for _, c := range cases {
		out, ok := lumaFromPixels(c.px, 1, 1, 8, c.bpp, c.bpp/8, c.info)
		if !ok || out[0] != want {
			t.Errorf("%s: %v %v, want %d", c.name, out, ok, want)
		}
	}
	if out, ok := lumaFromPixels([]byte{77}, 1, 1, 8, 8, 1, 0); !ok || out[0] != 77 {
		t.Errorf("gray: %v %v", out, ok)
	}
	if _, ok := lumaFromPixels([]byte{1, 2, 3, 4, 5, 6, 7, 8}, 1, 1, 16, 64, 8, 0); ok {
		t.Error("16 bits per component is refused, not misread")
	}
	if _, ok := lumaFromPixels([]byte{1, 2}, 2, 2, 8, 32, 8, 0); ok {
		t.Error("a short buffer is refused")
	}
}
