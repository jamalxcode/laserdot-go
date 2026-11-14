package media

// Fingerprints for the duplicate finder (spec §4.1). Deterministic, no
// machine learning:
//
//   - Exact: SHA-256 of the size and the first, middle and last 64 KiB,
//     then of the whole file only to confirm a match. (The Rust build used
//     BLAKE3; the hashes are cached only in this Mac's index, so the
//     algorithm is a local choice.)
//   - Images: a 64-bit perceptual hash (pHash: the low frequencies of a
//     32×32 DCT) of the image, read from the embedded EXIF thumbnail when
//     there is one.
//   - Video: the pHash of frames at 10 %, 50 % and 90 % of the duration,
//     decoded by an ffmpeg subprocess (one per frame, only when the
//     duplicate finder asks — never on the indexing path).

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	stdimage "image"
	"image/color"
	_ "image/gif" // register the decoder
	"image/jpeg"
	_ "image/png" // register the decoder
	"io"
	"math"
	"math/bits"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	_ "golang.org/x/image/bmp"  // register the decoder
	_ "golang.org/x/image/tiff" // register the decoder
	_ "golang.org/x/image/webp" // register the decoder
)

// Side of the grayscale thumbnail a pHash is computed from.
const Side = 32

// Gray is a 32×32 grayscale thumbnail, row-major, top row first.
type Gray [Side * Side]byte

// Window is the size of each sampled window of the quick exact hash.
const Window = 64 * 1024

// VideoPositions are where in a video the frames are taken, as fractions of
// the duration.
var VideoPositions = [3]float64{0.1, 0.5, 0.9}

// ---------------------------------------------------------------- exact

// QuickHash is SHA-256 of size + the first, middle and last Window. For a
// file of at most three windows it is the hash of the whole file (then
// FullHash gives nothing new).
func QuickHash(path string, size uint64) ([32]byte, error) {
	var sum [32]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	h.Write(binary.LittleEndian.AppendUint64(nil, size))
	if QuickIsFull(size) {
		if _, err := io.Copy(h, f); err != nil {
			return sum, err
		}
	} else {
		buf := make([]byte, Window)
		for _, off := range []uint64{0, size/2 - Window/2, size - Window} {
			if _, err := f.ReadAt(buf, int64(off)); err != nil {
				return sum, err
			}
			h.Write(buf)
		}
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// QuickIsFull says whether QuickHash already covers the whole file.
func QuickIsFull(size uint64) bool { return size <= 3*Window }

// FullHash is SHA-256 of the whole file (to confirm an exact match).
func FullHash(path string) ([32]byte, error) {
	var sum [32]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// ---------------------------------------------------------------- pHash

// Hamming is the number of differing bits.
func Hamming(a, b uint64) int { return bits.OnesCount64(a ^ b) }

// IsFlat: a nearly uniform frame (black, white, a solid colour) says
// nothing about the content and would match every other uniform frame.
func IsFlat(g *Gray) bool {
	n := float64(len(g))
	var sum float64
	for _, v := range g {
		sum += float64(v)
	}
	mean := sum / n
	var vr float64
	for _, v := range g {
		d := float64(v) - mean
		vr += d * d
	}
	return math.Sqrt(vr/n) < 4
}

// PHash: 2-D DCT-II of the 32×32 image, the 8×8 lowest frequencies
// without the DC row and column, one bit per coefficient above their
// median.
func PHash(g *Gray) uint64 {
	const k = 8
	var cos [k + 1][Side]float64
	for u := range cos {
		for x := range Side {
			cos[u][x] = math.Cos(float64(2*x+1) * float64(u) * math.Pi / float64(2*Side))
		}
	}
	// Rows first: t[u][y] = Σx cos[u+1][x]·g[y][x].
	var t [k][Side]float64
	for u := range k {
		for y := range Side {
			var s float64
			for x := range Side {
				s += float64(g[y*Side+x]) * cos[u+1][x]
			}
			t[u][y] = s
		}
	}
	var coeffs [k * k]float64
	for u := range k {
		for v := range k {
			var s float64
			for y := range Side {
				s += cos[v+1][y] * t[u][y]
			}
			coeffs[v*k+u] = s
		}
	}
	sorted := coeffs
	slices.Sort(sorted[:])
	median := (sorted[k*k/2-1] + sorted[k*k/2]) / 2
	var h uint64
	for i, c := range coeffs {
		if c > median {
			h |= 1 << i
		}
	}
	return h
}

// Downscale area-averages a luma image down to 32×32.
func Downscale(luma []byte, w, h int) (Gray, bool) {
	var out Gray
	if w <= 0 || h <= 0 || len(luma) < w*h {
		return out, false
	}
	for oy := range Side {
		y0 := oy * h / Side
		y1 := min(max((oy+1)*h/Side, y0+1), h)
		for ox := range Side {
			x0 := ox * w / Side
			x1 := min(max((ox+1)*w/Side, x0+1), w)
			var sum uint64
			for y := y0; y < y1; y++ {
				for _, v := range luma[y*w+x0 : y*w+x1] {
					sum += uint64(v)
				}
			}
			out[oy*Side+ox] = byte(sum / uint64((y1-y0)*(x1-x0)))
		}
	}
	return out, true
}

// Orient applies an EXIF orientation (1–8) to a square thumbnail. Squashing
// to a square commutes with 90° turns, so this equals the upright image.
func Orient(g *Gray, orientation uint32) Gray {
	const n = Side - 1
	var out Gray
	for y := range Side {
		for x := range Side {
			sx, sy := x, y
			switch orientation {
			case 2:
				sx, sy = n-x, y
			case 3:
				sx, sy = n-x, n-y
			case 4:
				sx, sy = x, n-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, n-x
			case 7:
				sx, sy = n-y, n-x
			case 8:
				sx, sy = n-y, x
			}
			out[y*Side+x] = g[sy*Side+sx]
		}
	}
	return out
}

// ---------------------------------------------------------------- images

// PortableImage: image types the pure-Go decoders read.
func PortableImage(ext string) bool {
	switch ext {
	case "jpg", "jpeg", "jpe", "png", "gif", "webp", "bmp", "tif", "tiff":
		return true
	}
	return false
}

// HashableImage: image types that have a perceptual hash here. HEIC, AVIF
// and camera RAW need ImageIO (the Rust build on macOS) and have none in
// this build.
func HashableImage(ext string) bool { return PortableImage(ext) }

// ImageGray is the upright 32×32 grayscale image; ok is false if it cannot
// be decoded.
func ImageGray(path, ext string) (Gray, bool) {
	var none Gray
	if !PortableImage(ext) {
		return none, false
	}
	fh, err := os.Open(path)
	if err != nil {
		return none, false
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return none, false
	}
	f := file{fh, st.Size()}
	orientation, thumb := exifOrientationAndThumbnail(f)
	if thumb != nil {
		if w, h, ok := imageSize(f); ok {
			if g, ok := decodeJPEG(thumb, int(w), int(h)); ok {
				return Orient(&g, orientation), true
			}
		}
	}
	var g Gray
	var ok bool
	switch ext {
	case "jpg", "jpeg", "jpe":
		data, err := os.ReadFile(path)
		if err != nil {
			return none, false
		}
		g, ok = decodeJPEG(data, 0, 0)
	default:
		if _, err := fh.Seek(0, io.SeekStart); err != nil {
			return none, false
		}
		px, decodable := decodablePixels(fh)
		if !decodable {
			return none, false
		}
		defer gateBig(px)()
		if _, err := fh.Seek(0, io.SeekStart); err != nil {
			return none, false
		}
		img, _, err := stdimage.Decode(fh)
		if err != nil {
			return none, false
		}
		w, h, luma := lumaOf(img, 2126, 7152, 722)
		g, ok = Downscale(luma, w, h)
	}
	if !ok {
		return none, false
	}
	return Orient(&g, orientation), true
}

// maxPixels bounds what a decode may allocate (a hostile header can claim
// any size).
const maxPixels = 200 << 20

// decodablePixels: the image's pixel count, and whether it may be decoded.
func decodablePixels(r io.Reader) (int64, bool) {
	cfg, _, err := stdimage.DecodeConfig(r)
	px := int64(cfg.Width) * int64(cfg.Height)
	return px, err == nil && cfg.Width > 0 && cfg.Height > 0 && px <= maxPixels
}

// bigPixels: above this, a decode needs hundreds of MB; such images are
// decoded one at a time, so a folder of huge scans or panoramas cannot fill
// the memory with eight of them at once (the Mac then crawls).
const bigPixels = 24 << 20

var bigDecode = make(chan struct{}, 1)

// gateBig waits for the big-image slot when px is large; call the result to
// release it.
func gateBig(px int64) func() {
	if px <= bigPixels {
		return func() {}
	}
	bigDecode <- struct{}{}
	return func() { <-bigDecode }
}

// exifOrientationAndThumbnail: EXIF orientation (1 when absent) and the
// embedded JPEG thumbnail.
func exifOrientationAndThumbnail(f file) (uint32, []byte) {
	ex := findExif(f.at(0, exifWindow))
	if ex == nil {
		return 1, nil
	}
	o, ok := ex.uintOf(ctxTIFF, 0x112)
	if !ok {
		o = 1
	}
	return o, ex.thumbnail()
}

// lumaOf converts an image to 8-bit luma with the given weights (per
// 10000) of R, G and B. JPEG-style Y planes are used as they are.
func lumaOf(img stdimage.Image, wr, wg, wb uint32) (int, int, []byte) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	luma := make([]byte, w*h)
	switch m := img.(type) {
	case *stdimage.Gray:
		for y := range h {
			copy(luma[y*w:(y+1)*w], m.Pix[m.PixOffset(b.Min.X, b.Min.Y+y):])
		}
	case *stdimage.YCbCr:
		for y := range h {
			copy(luma[y*w:(y+1)*w], m.Y[m.YOffset(b.Min.X, b.Min.Y+y):])
		}
	default:
		for y := range h {
			for x := range w {
				c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
				luma[y*w+x] = byte((wr*uint32(c.R) + wg*uint32(c.G) + wb*uint32(c.B)) / 10000)
			}
		}
	}
	return w, h, luma
}

// decodeJPEG decodes a JPEG to 32×32 grayscale. aw:ah, when set: crop to
// that aspect first (thumbnails are often padded with bars to a fixed 4:3
// or 16:9 frame). Images smaller than 32×32 are refused.
func decodeJPEG(data []byte, aw, ah int) (Gray, bool) {
	var none Gray
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < Side || cfg.Height < Side || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return none, false
	}
	defer gateBig(int64(cfg.Width) * int64(cfg.Height))()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return none, false
	}
	var w, h int
	var luma []byte
	if c, ok := img.(*stdimage.CMYK); ok {
		b := c.Bounds()
		w, h = b.Dx(), b.Dy()
		luma = make([]byte, w*h)
		for y := range h {
			for x := range w {
				p := c.Pix[c.PixOffset(b.Min.X+x, b.Min.Y+y):]
				ink := (uint32(p[0]) + uint32(p[1]) + uint32(p[2])) / 3
				luma[y*w+x] = byte((255 - ink) * (255 - uint32(p[3])) / 255)
			}
		}
	} else {
		w, h, luma = lumaOf(img, 2990, 5870, 1140)
	}
	x0, y0, cw, ch := 0, 0, w, h
	if aw > 0 && ah > 0 {
		want := float64(aw) / float64(ah)
		have := float64(w) / float64(h)
		if math.Abs(want/have-1) > 0.02 {
			if want > have {
				ch = min(max(int(math.Round(float64(w)/want)), 1), h)
				y0 = (h - ch) / 2
			} else {
				cw = min(max(int(math.Round(float64(h)*want)), 1), w)
				x0 = (w - cw) / 2
			}
		}
	}
	if cw < Side || ch < Side {
		return none, false
	}
	crop := make([]byte, 0, cw*ch)
	for y := y0; y < y0+ch; y++ {
		crop = append(crop, luma[y*w+x0:y*w+x0+cw]...)
	}
	return Downscale(crop, cw, ch)
}

// ---------------------------------------------------------------- video

// VideoBackend says how video frames are decoded here ("" when they cannot
// be).
func VideoBackend() string {
	if p := ffmpegPath(); p != "" {
		return "ffmpeg (" + p + ")"
	}
	return ""
}

// VideoFrames are the frames at VideoPositions of a video durationMS long.
func VideoFrames(path string, durationMS uint64) ([]Gray, error) {
	if durationMS == 0 {
		return nil, errors.New("unknown duration")
	}
	exe := ffmpegPath()
	if exe == "" {
		return nil, errors.New("no video decoder (ffmpeg not found)")
	}
	var out []Gray
	for _, p := range VideoPositions {
		g, err := ffmpegFrame(exe, path, float64(durationMS)/1000*p)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// ffmpegPath is ffmpeg, found the same way as for Tier 2 (see Tool).
func ffmpegPath() string { return Tool("ffmpeg") }

func ffmpegFrame(exe, path string, secs float64) (Gray, error) {
	var g Gray
	cmd := exec.Command(exe, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-ss", strconv.FormatFloat(secs, 'f', 3, 64), "-i", path,
		"-frames:v", "1", "-vf", "scale=32:32:flags=area,format=gray", "-f", "rawvideo", "pipe:1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil && stdout.Len() < Side*Side {
		if _, ok := err.(*exec.ExitError); !ok {
			return g, fmt.Errorf("ffmpeg: %w", err)
		}
	}
	if stdout.Len() < Side*Side {
		return g, fmt.Errorf("ffmpeg could not decode a frame: %s", strings.TrimSpace(stderr.String()))
	}
	copy(g[:], stdout.Bytes())
	return g, nil
}
