package query

import (
	"strings"

	"github.com/jamalxcode/laserdot-go/internal/fold"
)

// File-type words (spec §2.7 "type word"): like Spotlight, a plain word that
// names a file type means that type rather than text in the name.
//   jpg, avi, pdf …         → ext:  (with the type's other spellings: jpg = jpg,jpeg)
//   video, photos, music …  → kind:
// Only unquoted words without wildcards count; "jpg" in quotes searches the
// name, and -jpg excludes the type. Words that are also ordinary English
// (go, key, pages, raw, app, ts) are deliberately not type words.

// extWords: extension → the spellings it stands for.
var extWords = map[string][]string{}

func init() {
	groups := [][]string{
		// images
		{"jpg", "jpeg", "jpe"}, {"png"}, {"gif"}, {"heic", "heif"}, {"tif", "tiff"}, {"bmp"}, {"webp"},
		{"svg"}, {"psd"}, {"avif"}, {"cr2"}, {"cr3"}, {"nef"}, {"arw"}, {"dng"}, {"orf"}, {"rw2"},
		// video
		{"avi"}, {"mp4"}, {"m4v"}, {"mkv"}, {"mov"}, {"wmv"}, {"flv"}, {"webm"}, {"mpg", "mpeg"},
		{"m2ts", "mts"}, {"vob"}, {"3gp"}, {"divx"},
		// audio
		{"mp3"}, {"flac"}, {"wav"}, {"aac"}, {"m4a"}, {"ogg", "oga"}, {"opus"}, {"aiff", "aif"}, {"wma"},
		// documents
		{"pdf"}, {"doc", "docx"}, {"xls", "xlsx"}, {"ppt", "pptx", "pps", "ppsx"}, {"epub"}, {"rtf"}, {"txt"},
		{"csv"}, {"odt"},
		// archives and disk images
		{"zip"}, {"rar"}, {"7z"}, {"dmg"}, {"iso"}, {"tgz"},
	}
	for _, g := range groups {
		for _, e := range g {
			extWords[e] = g
		}
	}
	// Words for a family of formats.
	for w, g := range map[string][]string{
		"powerpoint": {"ppt", "pptx", "pps", "ppsx", "key"}, "presentation": {"ppt", "pptx", "pps", "ppsx", "key"},
		"presentations": {"ppt", "pptx", "pps", "ppsx", "key"}, "keynote": {"key"},
		"excel": {"xls", "xlsx", "csv", "numbers"}, "spreadsheet": {"xls", "xlsx", "csv", "numbers"},
		"spreadsheets": {"xls", "xlsx", "csv", "numbers"},
		"اكسل": {"xls", "xlsx", "csv", "numbers"},
	} {
		extWords[fold.Fold(w)] = g
	}
}

// kindWords: English and Arabic (already folded) words → kind values.
var kindWords = map[string]string{
	"video": "video", "videos": "video", "movie": "video", "movies": "video", "film": "video", "films": "video",
	"photo": "image", "photos": "image", "picture": "image", "pictures": "image", "image": "image",
	"images": "image", "graphics": "image", "pics": "image",
	"music": "audio", "song": "audio", "songs": "audio", "audio": "audio",
	"document": "document", "documents": "document",
	"archive": "archive", "archives": "archive",
	"فيديو": "video", "فيديوهات": "video", "فلم": "video", "فيلم": "video", "افلام": "video",
	"صوره": "image", "صور": "image",
	"موسيقي": "audio", "اغاني": "audio", "اغنيه": "audio", "صوت": "audio",
	"مستند": "document", "مستندات": "document",
}

func init() {
	// Keys are compared with folded words, so fold them the same way.
	folded := map[string]string{}
	for w, k := range kindWords {
		folded[fold.Fold(w)] = k
	}
	kindWords = folded
}

// typeFilter turns a plain word into ext:/kind: when it names a file type.
func (p *parser) typeFilter(word string, span Span) (*Filter, bool) {
	if exts, ok := extWords[word]; ok {
		def := Lookup("ext", NoScope)
		v, err := p.value(def, []rune(strings.Join(exts, ",")), span)
		if err == nil {
			return &Filter{Def: def, Scope: NoScope, Value: v, Span: span}, true
		}
	}
	if k, ok := kindWords[word]; ok {
		def := Lookup("kind", NoScope)
		v, err := p.value(def, []rune(k), span)
		if err == nil {
			return &Filter{Def: def, Scope: NoScope, Value: v, Span: span}, true
		}
	}
	return nil, false
}

// TypeWordMeaning is what a type word stands for ("ext:jpg,jpeg"), for the
// hint line; "" when word is not one.
func TypeWordMeaning(word string) string {
	w := fold.Fold(word)
	if exts, ok := extWords[w]; ok {
		return "ext:" + strings.Join(exts, ",")
	}
	if k, ok := kindWords[w]; ok {
		return "kind:" + k
	}
	return ""
}

// TypeWords maps every type word (folded) to what it stands for, for the
// app's offline guidance (app/scripts/registry).
func TypeWords() map[string]string {
	out := map[string]string{}
	for w, exts := range extWords {
		out[w] = "ext:" + strings.Join(exts, ",")
	}
	for w, k := range kindWords {
		out[w] = "kind:" + k
	}
	return out
}
