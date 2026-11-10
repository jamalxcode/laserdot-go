// Code generated from the Rust registry (laserdot-query/src/registry.rs); keep in step with the spec.

package query

// Filters is the registry: every filter, declared once (spec §8.8).
var Filters = []FilterDef{
	{ID: FKind, Key: "kind", Section: SectionFile, Value: ValueType{Kind: VEnum, Values: Kinds}, Scopes: nil,
		Description: "type of file", Example: "kind:video"},
	{ID: FExt, Key: "ext", Section: SectionFile, Value: ValueType{Kind: VText, Exact: true}, Scopes: nil,
		Description: "file extension", Example: "ext:mkv,mp4"},
	{ID: FFsize, Key: "fsize", Section: SectionFile, Value: ValueType{Kind: VSize}, Scopes: nil,
		Description: "file size", Example: "fsize:>4gb"},
	{ID: FContainer, Key: "container", Section: SectionFile, Value: ValueType{Kind: VText, Exact: true}, Scopes: nil,
		Description: "container format", Example: "container:mkv"},
	{ID: FModified, Key: "modified", Section: SectionFile, Value: ValueType{Kind: VDate}, Scopes: nil,
		Description: "date modified", Example: "modified:2025-01..2025-06"},
	{ID: FCreated, Key: "created", Section: SectionFile, Value: ValueType{Kind: VDate}, Scopes: nil,
		Description: "date created", Example: "created:2024"},
	{ID: FSince, Key: "since", Section: SectionFile, Value: ValueType{Kind: VDate}, Scopes: nil,
		Description: "modified on or after", Example: "since:7d"},
	{ID: FBefore, Key: "before", Section: SectionFile, Value: ValueType{Kind: VDate}, Scopes: nil,
		Description: "modified before", Example: "before:2020-01-01"},
	{ID: FIn, Key: "in", Section: SectionFile, Value: ValueType{Kind: VPath}, Scopes: nil,
		Description: "inside this folder", Example: "in:~/Movies"},
	{ID: FVolume, Key: "volume", Section: SectionFile, Value: ValueType{Kind: VText, Exact: false}, Scopes: nil,
		Description: "on this drive (`local` = this Mac)", Example: "volume:archive"},
	{ID: FContent, Key: "content", Section: SectionFile, Value: ValueType{Kind: VText, Exact: false}, Scopes: nil,
		Description: "these words inside the file (text, code, PDF; word match, not regex)", Example: "content:\"quarterly report\""},
	{ID: FLocal, Key: "local", Section: SectionFile, Value: ValueType{Kind: VBool}, Scopes: nil,
		Description: "physically on disk (not an iCloud placeholder)", Example: "local:true"},
	{ID: FHidden, Key: "hidden", Section: SectionFile, Value: ValueType{Kind: VBool}, Scopes: nil,
		Description: "hidden (dot) files and folders", Example: "hidden:false"},
	{ID: FTag, Key: "tag", Section: SectionFile, Value: ValueType{Kind: VText, Exact: false}, Scopes: nil,
		Description: "Finder tag", Example: "tag:red"},
	{ID: FRes, Key: "res", Section: SectionVideo, Value: ValueType{Kind: VEnum, Values: Res}, Scopes: nil,
		Description: "resolution class by long edge", Example: "res:4k"},
	{ID: FWidth, Key: "width", Section: SectionVideo, Value: ValueType{Kind: VNumber}, Scopes: nil,
		Description: "pixel width (display orientation)", Example: "width:>=3840"},
	{ID: FHeight, Key: "height", Section: SectionVideo, Value: ValueType{Kind: VNumber}, Scopes: nil,
		Description: "pixel height (display orientation)", Example: "height:1080"},
	{ID: FOrientation, Key: "orientation", Section: SectionVideo, Value: ValueType{Kind: VEnum, Values: []string{"portrait", "landscape", "square"}}, Scopes: nil,
		Description: "shape of the frame", Example: "orientation:portrait"},
	{ID: FCodec, Key: "codec", Section: SectionVideo, Value: ValueType{Kind: VText, Exact: true}, Scopes: []Scope{Video},
		Description: "video codec", Example: "codec:hevc"},
	{ID: FProfile, Key: "profile", Section: SectionVideo, Value: ValueType{Kind: VText, Exact: true}, Scopes: []Scope{Video},
		Description: "codec profile", Example: "profile:\"main 10\""},
	{ID: FPixFmt, Key: "pix_fmt", Section: SectionVideo, Value: ValueType{Kind: VText, Exact: true}, Scopes: []Scope{Video},
		Description: "pixel format", Example: "pix_fmt:yuv420p10le"},
	{ID: FBitDepth, Key: "bit_depth", Section: SectionVideo, Value: ValueType{Kind: VNumber}, Scopes: []Scope{Video},
		Description: "bits per colour channel", Example: "bit_depth:10"},
	{ID: FHdr, Key: "hdr", Section: SectionVideo, Value: ValueType{Kind: VEnum, Values: []string{"true", "false", "hdr10", "hdr10+", "hlg", "dv"}}, Scopes: []Scope{Video},
		Description: "HDR format", Example: "hdr:dv"},
	{ID: FFps, Key: "fps", Section: SectionVideo, Value: ValueType{Kind: VNumber}, Scopes: []Scope{Video},
		Description: "frame rate", Example: "fps:>=50"},
	{ID: FVfr, Key: "vfr", Section: SectionVideo, Value: ValueType{Kind: VBool}, Scopes: []Scope{Video},
		Description: "variable frame rate", Example: "vfr:true"},
	{ID: FDuration, Key: "duration", Section: SectionVideo, Value: ValueType{Kind: VDuration}, Scopes: nil,
		Description: "running time", Example: "duration:>2h"},
	{ID: FBitrate, Key: "bitrate", Section: SectionVideo, Value: ValueType{Kind: VBitrate}, Scopes: nil,
		Description: "total bit rate", Example: "bitrate:>20mbps"},
	{ID: FBpp, Key: "bpp", Section: SectionVideo, Value: ValueType{Kind: VNumber}, Scopes: []Scope{Video},
		Description: "bits per pixel per frame (quality)", Example: "bpp:<0.05"},
	{ID: FAspectRatio, Key: "ar", Section: SectionVideo, Value: ValueType{Kind: VText, Exact: true}, Scopes: []Scope{Video},
		Description: "display aspect ratio", Example: "ar:21:9"},
	{ID: FChannels, Key: "channels", Section: SectionAudio, Value: ValueType{Kind: VEnum, Values: []string{"mono", "stereo", "5.1", "7.1"}}, Scopes: []Scope{Audio},
		Description: "channel layout", Example: "channels:5.1"},
	{ID: FAtmos, Key: "atmos", Section: SectionAudio, Value: ValueType{Kind: VBool}, Scopes: []Scope{Audio},
		Description: "Dolby Atmos", Example: "atmos:true"},
	{ID: FLossless, Key: "lossless", Section: SectionAudio, Value: ValueType{Kind: VBool}, Scopes: []Scope{Audio},
		Description: "lossless codec (flac, alac, truehd, dts-hd ma)", Example: "lossless:true"},
	{ID: FLang, Key: "lang", Section: SectionAudio, Value: ValueType{Kind: VText, Exact: true}, Scopes: []Scope{Audio, Subtitle},
		Description: "track language", Example: "lang:ar"},
	{ID: FAcodec, Key: "acodec", Section: SectionAudio, Value: ValueType{Kind: VText, Exact: true}, Scopes: []Scope{Audio},
		Description: "audio codec", Example: "acodec:truehd"},
	{ID: FSampleRate, Key: "sample_rate", Section: SectionAudio, Value: ValueType{Kind: VNumber}, Scopes: []Scope{Audio},
		Description: "sample rate in Hz", Example: "sample_rate:>=96000"},
	{ID: FTrackCount, Key: "count", Section: SectionAudio, Value: ValueType{Kind: VNumber}, Scopes: []Scope{Audio, Video, Subtitle},
		Description: "number of tracks of a type", Example: "a.count:>=2"},
	{ID: FForced, Key: "forced", Section: SectionSubtitles, Value: ValueType{Kind: VBool}, Scopes: []Scope{Subtitle},
		Description: "forced subtitle track", Example: "s.forced:true"},
	{ID: FSubFormat, Key: "format", Section: SectionSubtitles, Value: ValueType{Kind: VEnum, Values: []string{"srt", "ass", "pgs", "vobsub", "webvtt"}}, Scopes: []Scope{Subtitle},
		Description: "subtitle format", Example: "s.format:pgs"},
	{ID: FSidecar, Key: "sidecar", Section: SectionSubtitles, Value: ValueType{Kind: VBool}, Scopes: nil,
		Description: "has a sidecar subtitle file", Example: "sidecar:true"},
	{ID: FMegapixels, Key: "megapixels", Section: SectionImages, Value: ValueType{Kind: VNumber}, Scopes: nil,
		Description: "image size in megapixels", Example: "megapixels:>20"},
	{ID: FRaw, Key: "raw", Section: SectionImages, Value: ValueType{Kind: VBool}, Scopes: nil,
		Description: "camera RAW format", Example: "raw:true"},
	{ID: FCamera, Key: "camera", Section: SectionImages, Value: ValueType{Kind: VText, Exact: false}, Scopes: nil,
		Description: "camera model (EXIF)", Example: "camera:*R5*"},
	{ID: FLens, Key: "lens", Section: SectionImages, Value: ValueType{Kind: VText, Exact: false}, Scopes: nil,
		Description: "lens model (EXIF)", Example: "lens:\"24-70\""},
	{ID: FIso, Key: "iso", Section: SectionImages, Value: ValueType{Kind: VNumber}, Scopes: nil,
		Description: "ISO (EXIF)", Example: "iso:>=3200"},
	{ID: FAperture, Key: "aperture", Section: SectionImages, Value: ValueType{Kind: VNumber}, Scopes: nil,
		Description: "f-number (EXIF)", Example: "aperture:<=2.8"},
	{ID: FFocal, Key: "focal", Section: SectionImages, Value: ValueType{Kind: VNumber}, Scopes: nil,
		Description: "focal length in mm (EXIF)", Example: "focal:85"},
	{ID: FTaken, Key: "taken", Section: SectionImages, Value: ValueType{Kind: VDate}, Scopes: nil,
		Description: "capture date (EXIF)", Example: "taken:2024"},
	{ID: FGps, Key: "gps", Section: SectionImages, Value: ValueType{Kind: VBool}, Scopes: nil,
		Description: "has GPS location", Example: "gps:true"},
	{ID: FLive, Key: "live", Section: SectionImages, Value: ValueType{Kind: VBool}, Scopes: nil,
		Description: "Live Photo (HEIC + MOV pair)", Example: "live:true"},
	{ID: FScreenshot, Key: "screenshot", Section: SectionImages, Value: ValueType{Kind: VBool}, Scopes: nil,
		Description: "screenshot", Example: "screenshot:true"},
	{ID: FArtist, Key: "artist", Section: SectionMusic, Value: ValueType{Kind: VText, Exact: false}, Scopes: nil,
		Description: "artist tag", Example: "artist:\"fairouz\""},
	{ID: FAlbum, Key: "album", Section: SectionMusic, Value: ValueType{Kind: VText, Exact: false}, Scopes: nil,
		Description: "album tag", Example: "album:*live*"},
	{ID: FGenre, Key: "genre", Section: SectionMusic, Value: ValueType{Kind: VText, Exact: false}, Scopes: nil,
		Description: "genre tag", Example: "genre:electronic"},
	{ID: FTitle, Key: "title", Section: SectionMusic, Value: ValueType{Kind: VText, Exact: false}, Scopes: nil,
		Description: "title tag", Example: "title:\"ya rayah\""},
	{ID: FYear, Key: "year", Section: SectionMusic, Value: ValueType{Kind: VNumber}, Scopes: nil,
		Description: "year tag", Example: "year:1970..1979"},
}

// FilterID identifies a filter for the executor.
type FilterID int

const (
	FKind FilterID = iota
	FExt
	FFsize
	FContainer
	FModified
	FCreated
	FSince
	FBefore
	FIn
	FVolume
	FContent
	FLocal
	FHidden
	FTag
	FRes
	FWidth
	FHeight
	FOrientation
	FCodec
	FProfile
	FPixFmt
	FBitDepth
	FHdr
	FFps
	FVfr
	FDuration
	FBitrate
	FBpp
	FAspectRatio
	FChannels
	FAtmos
	FLossless
	FLang
	FAcodec
	FSampleRate
	FTrackCount
	FForced
	FSubFormat
	FSidecar
	FMegapixels
	FRaw
	FCamera
	FLens
	FIso
	FAperture
	FFocal
	FTaken
	FGps
	FLive
	FScreenshot
	FArtist
	FAlbum
	FGenre
	FTitle
	FYear
)
