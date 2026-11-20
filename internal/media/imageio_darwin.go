//go:build darwin

package media

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// macOS's ImageIO reads the picture types the pure-Go decoders cannot —
// HEIC/HEIF (every iPhone photo), AVIF and camera RAW — so they get a
// perceptual hash and take part in "similar pictures". It is asked for a
// small, upright thumbnail (an embedded one when the file has it), which is
// all a 32×32 hash needs. Bound at run time with purego (no cgo), as the
// FSEvents watcher is.

var (
	ioOnce sync.Once
	ioOK   bool

	cfStringCreateWithCString      func(alloc uintptr, s *byte, enc uint32) uintptr
	cfURLCreateWithFileSystemPath  func(alloc, path uintptr, style int, isDir bool) uintptr
	cfDictionaryCreate             func(alloc uintptr, keys, values *uintptr, n int, keyCB, valueCB uintptr) uintptr
	cfNumberCreate                 func(alloc uintptr, typ int, value *int32) uintptr
	cfDataGetLength                func(data uintptr) int
	cfDataGetBytePtr               func(data uintptr) *byte
	cfRelease                      func(obj uintptr)
	cgImageSourceCreateWithURL     func(url, opts uintptr) uintptr
	cgImageSourceCreateThumbnailAt func(src uintptr, index uint, opts uintptr) uintptr
	cgImageGetWidth                func(img uintptr) uint
	cgImageGetHeight               func(img uintptr) uint
	cgImageGetBitsPerComponent     func(img uintptr) uint
	cgImageGetBitsPerPixel         func(img uintptr) uint
	cgImageGetBytesPerRow          func(img uintptr) uint
	cgImageGetBitmapInfo           func(img uintptr) uint32
	cgImageGetDataProvider         func(img uintptr) uintptr
	cgDataProviderCopyData         func(provider uintptr) uintptr

	thumbOptions uintptr // the thumbnail options dictionary, made once
)

const (
	cfStringEncodingUTF8 = 0x08000100
	cfNumberIntType      = 9
	thumbMaxPixels       = 160
)

func loadImageIO() bool {
	ioOnce.Do(func() {
		open := func(path string) uintptr {
			h, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			if err != nil {
				return 0
			}
			return h
		}
		cf := open("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
		cg := open("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
		io := open("/System/Library/Frameworks/ImageIO.framework/ImageIO")
		if cf == 0 || cg == 0 || io == 0 {
			return
		}
		ok := true
		bind := func(lib uintptr, fptr any, name string) {
			sym, err := purego.Dlsym(lib, name)
			if err != nil {
				ok = false
				return
			}
			purego.RegisterFunc(fptr, sym)
		}
		bind(cf, &cfStringCreateWithCString, "CFStringCreateWithCString")
		bind(cf, &cfURLCreateWithFileSystemPath, "CFURLCreateWithFileSystemPath")
		bind(cf, &cfDictionaryCreate, "CFDictionaryCreate")
		bind(cf, &cfNumberCreate, "CFNumberCreate")
		bind(cf, &cfDataGetLength, "CFDataGetLength")
		bind(cf, &cfDataGetBytePtr, "CFDataGetBytePtr")
		bind(cf, &cfRelease, "CFRelease")
		bind(io, &cgImageSourceCreateWithURL, "CGImageSourceCreateWithURL")
		bind(io, &cgImageSourceCreateThumbnailAt, "CGImageSourceCreateThumbnailAtIndex")
		bind(cg, &cgImageGetWidth, "CGImageGetWidth")
		bind(cg, &cgImageGetHeight, "CGImageGetHeight")
		bind(cg, &cgImageGetBitsPerComponent, "CGImageGetBitsPerComponent")
		bind(cg, &cgImageGetBitsPerPixel, "CGImageGetBitsPerPixel")
		bind(cg, &cgImageGetBytesPerRow, "CGImageGetBytesPerRow")
		bind(cg, &cgImageGetBitmapInfo, "CGImageGetBitmapInfo")
		bind(cg, &cgImageGetDataProvider, "CGImageGetDataProvider")
		bind(cg, &cgDataProviderCopyData, "CGDataProviderCopyData")
		if !ok {
			return
		}
		// The option keys and values are exported constants: their
		// addresses hold the objects.
		deref := func(lib uintptr, name string) uintptr {
			sym, err := purego.Dlsym(lib, name)
			if err != nil || sym == 0 {
				ok = false
				return 0
			}
			return **(**uintptr)(unsafe.Pointer(&sym)) // the object the constant holds (as the FSEvents binding does)
		}
		keyCB, err1 := purego.Dlsym(cf, "kCFTypeDictionaryKeyCallBacks")
		valueCB, err2 := purego.Dlsym(cf, "kCFTypeDictionaryValueCallBacks")
		yes := deref(cf, "kCFBooleanTrue")
		kIfAbsent := deref(io, "kCGImageSourceCreateThumbnailFromImageIfAbsent")
		kMax := deref(io, "kCGImageSourceThumbnailMaxPixelSize")
		kTransform := deref(io, "kCGImageSourceCreateThumbnailWithTransform")
		if !ok || err1 != nil || err2 != nil {
			return
		}
		size := int32(thumbMaxPixels)
		maxPixels := cfNumberCreate(0, cfNumberIntType, &size)
		keys := []uintptr{kIfAbsent, kMax, kTransform}
		values := []uintptr{yes, maxPixels, yes}
		thumbOptions = cfDictionaryCreate(0, &keys[0], &values[0], len(keys), keyCB, valueCB)
		ioOK = thumbOptions != 0
	})
	return ioOK
}

func cString(s string) *byte {
	b := append([]byte(s), 0)
	return &b[0]
}

// imageIOGray: the upright 32×32 grayscale picture through ImageIO.
func imageIOGray(path string) (Gray, bool) {
	var none Gray
	if !loadImageIO() {
		return none, false
	}
	str := cfStringCreateWithCString(0, cString(path), cfStringEncodingUTF8)
	if str == 0 {
		return none, false
	}
	defer cfRelease(str)
	url := cfURLCreateWithFileSystemPath(0, str, 0, false)
	if url == 0 {
		return none, false
	}
	defer cfRelease(url)
	src := cgImageSourceCreateWithURL(url, 0)
	if src == 0 {
		return none, false
	}
	defer cfRelease(src)
	img := cgImageSourceCreateThumbnailAt(src, 0, thumbOptions)
	if img == 0 {
		return none, false
	}
	defer cfRelease(img)
	w, h := int(cgImageGetWidth(img)), int(cgImageGetHeight(img))
	bpc, bpp, stride := int(cgImageGetBitsPerComponent(img)), int(cgImageGetBitsPerPixel(img)), int(cgImageGetBytesPerRow(img))
	info := cgImageGetBitmapInfo(img)
	if w <= 0 || h <= 0 || w > 4096 || h > 4096 {
		return none, false
	}
	data := cgDataProviderCopyData(cgImageGetDataProvider(img))
	if data == 0 {
		return none, false
	}
	defer cfRelease(data)
	n := cfDataGetLength(data)
	if n < stride*(h-1)+w*bpp/8 {
		return none, false
	}
	px := unsafe.Slice(cfDataGetBytePtr(data), n)
	luma, ok := lumaFromPixels(px, w, h, bpc, bpp, stride, info)
	if !ok {
		return none, false
	}
	return Downscale(luma, w, h)
}
