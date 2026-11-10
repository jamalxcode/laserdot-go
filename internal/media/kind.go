// Package media classifies files and (from G2) parses media headers
// (spec §3).
package media

import "strings"

// Kind is a file kind (kind: filter). Its value is stored in the index.
type Kind uint8

const (
	Video Kind = iota
	Audio
	Image
	Document
	Archive
	Code
	App
	Folder
	Other
)

// Kinds in value order; their names are query.Kinds.
var Kinds = []Kind{Video, Audio, Image, Document, Archive, Code, App, Folder, Other}

func (k Kind) String() string {
	return [...]string{"video", "audio", "image", "document", "archive", "code", "app", "folder", "other"}[k]
}

func set(exts ...string) map[string]bool {
	m := make(map[string]bool, len(exts))
	for _, e := range exts {
		m[e] = true
	}
	return m
}

var (
	video = set("mp4", "m4v", "mov", "mkv", "webm", "avi", "wmv", "flv", "mpg", "mpeg", "m2ts", "mts", "ts",
		"3gp", "ogv", "vob", "divx", "rm", "rmvb", "asf", "f4v")
	audio = set("mp3", "m4a", "aac", "flac", "wav", "aiff", "aif", "alac", "ogg", "oga", "opus", "wma", "ac3",
		"eac3", "dts", "mka", "ape", "wv", "caf", "mid", "midi", "thd")
	image = set("jpg", "jpeg", "png", "gif", "heic", "heif", "avif", "webp", "tif", "tiff", "bmp", "ico",
		"icns", "psd", "svg", "jxl", "exr", "hdr", "tga", "dds")
	// raw: camera RAW formats (raw:true, §3.4).
	raw = set("cr2", "cr3", "crw", "nef", "nrw", "arw", "srf", "sr2", "dng", "raf", "orf", "rw2", "pef",
		"srw", "x3f", "3fr", "iiq", "rwl", "erf", "mef", "mos")
	document = set("pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "pages", "numbers", "key", "odt", "ods",
		"odp", "rtf", "txt", "md", "markdown", "csv", "tsv", "epub", "mobi", "srt", "ass", "ssa", "vtt", "sub")
	archive = set("zip", "rar", "7z", "tar", "gz", "tgz", "bz2", "xz", "zst", "dmg", "iso", "pkg",
		"sparsebundle", "cab", "lz4")
	code = set("rs", "swift", "c", "h", "cc", "cpp", "hpp", "m", "mm", "py", "js", "mjs", "ts", "tsx", "jsx",
		"go", "java", "kt", "rb", "php", "sh", "zsh", "bash", "fish", "html", "css", "scss", "json",
		"yaml", "yml", "toml", "xml", "sql", "lua", "pl", "r", "cs", "dart", "zig", "ex", "exs", "hs",
		"ml", "vue", "svelte")
)

// Extension of a folded file name ("" if none; a leading dot is not one).
func Extension(foldedName string) string {
	i := strings.LastIndexByte(foldedName, '.')
	if i <= 0 {
		return ""
	}
	return foldedName[i+1:]
}

// Classify by folded extension. .ts is video (MPEG transport stream) only
// when large; small .ts files are TypeScript.
func Classify(ext string, isDir bool, size uint64) Kind {
	if isDir {
		if ext == "app" {
			return App
		}
		return Folder
	}
	if ext == "ts" {
		if size > 4*1024*1024 {
			return Video
		}
		return Code
	}
	switch {
	case video[ext]:
		return Video
	case audio[ext]:
		return Audio
	case image[ext] || raw[ext]:
		return Image
	case document[ext]:
		return Document
	case archive[ext]:
		return Archive
	case code[ext]:
		return Code
	}
	return Other
}

// IsRaw: a camera RAW format.
func IsRaw(ext string) bool { return raw[ext] }

// ResBucket is the resolution class by long edge (an index into the res:
// values sd, 720p, 1080p, 1440p, 4k, 8k), so portrait and letterboxed frames
// bucket correctly (1080×1920 and 1920×800 are both 1080p). ok is false
// when the dimensions are unknown.
func ResBucket(width, height uint32) (int, bool) {
	long := max(width, height)
	switch {
	case long == 0:
		return 0, false
	case long < 1280:
		return 0, true
	case long < 1920:
		return 1, true
	case long < 2560:
		return 2, true
	case long < 3840:
		return 3, true
	case long < 7680:
		return 4, true
	}
	return 5, true
}
