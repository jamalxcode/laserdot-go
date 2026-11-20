package media

// Pixel layouts as CoreGraphics describes them (CGBitmapInfo).
const (
	cgAlphaMask         = 0x1F
	cgByteOrderMask     = 0x7000
	cgByteOrder32Little = 0x2000
)

// lumaFromPixels turns an 8-bit-per-component pixel buffer (gray, RGB or
// RGBA/ARGB in either byte order) into luma with the same weights as the
// pure-Go path (Rec. 709). Other layouts: ok false.
func lumaFromPixels(px []byte, w, h, bpc, bpp, stride int, info uint32) ([]byte, bool) {
	if bpc != 8 || stride < w*bpp/8 || len(px) < stride*(h-1)+w*bpp/8 {
		return nil, false
	}
	out := make([]byte, w*h)
	lum := func(r, g, b byte) byte {
		return byte((2126*uint32(r) + 7152*uint32(g) + 722*uint32(b)) / 10000)
	}
	switch bpp {
	case 8, 16: // gray (and gray + alpha): the first byte is gray
		step := bpp / 8
		for y := 0; y < h; y++ {
			row := px[y*stride:]
			for x := 0; x < w; x++ {
				out[y*w+x] = row[x*step]
			}
		}
	case 24:
		for y := 0; y < h; y++ {
			row := px[y*stride:]
			for x := 0; x < w; x++ {
				p := row[x*3:]
				out[y*w+x] = lum(p[0], p[1], p[2])
			}
		}
	case 32:
		alpha := info & cgAlphaMask
		alphaFirst := alpha == 2 || alpha == 4 || alpha == 6 // premultiplied first, first, none-skip-first
		little := info&cgByteOrderMask == cgByteOrder32Little
		// Where R, G and B sit in each 4-byte pixel.
		var ri, gi, bi int
		switch {
		case !little && alphaFirst: // A R G B
			ri, gi, bi = 1, 2, 3
		case !little: // R G B A
			ri, gi, bi = 0, 1, 2
		case alphaFirst: // little-endian ARGB: B G R A in memory
			ri, gi, bi = 2, 1, 0
		default: // little-endian RGBA: A B G R in memory
			ri, gi, bi = 3, 2, 1
		}
		for y := 0; y < h; y++ {
			row := px[y*stride:]
			for x := 0; x < w; x++ {
				p := row[x*4:]
				out[y*w+x] = lum(p[ri], p[gi], p[bi])
			}
		}
	default:
		return nil, false
	}
	return out, true
}

// SystemImage: picture types only the system's decoder reads (ImageIO on
// macOS): HEIC/HEIF, AVIF and camera RAW.
func SystemImage(ext string) bool {
	switch ext {
	case "heic", "heif", "hif", "avif", "dng", "cr2", "cr3", "crw", "nef", "nrw", "arw", "srf", "sr2", "orf",
		"raf", "rw2", "rwl", "pef", "srw", "x3f", "3fr", "iiq", "erf", "mos", "mrw":
		return true
	}
	return false
}
