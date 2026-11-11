package query

import (
	"fmt"
	"strings"
	"time"

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
		"اكسل":         {"xls", "xlsx", "csv", "numbers"},
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
	"apps": "app", "تطبيقات": "app",
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
	for w, f := range filterWords {
		out[w] = f
	}
	return out
}

// Plain-language words (spec §2.7 "type word", v1.21): dates, sizes,
// resolutions and screenshots, written as people say them. Each stands for
// a filter, shown on the hint line; quotes search names for the word.
var filterWords = map[string]string{
	"today": "modified:today", "yesterday": "modified:yesterday", "recent": "since:7d",
	"big": "fsize:>100mb", "large": "fsize:>100mb", "huge": "fsize:>1gb",
	"small": "fsize:<100kb", "tiny": "fsize:<10kb",
	"4k": "res:4k", "8k": "res:8k", "1080p": "res:1080p", "720p": "res:720p",
	"hdr": "hdr:true", "screenshot": "screenshot:true", "screenshots": "screenshot:true",
	"اليوم": "modified:today", "امس": "modified:yesterday", "حديث": "since:7d",
	"كبير": "fsize:>100mb", "كبيره": "fsize:>100mb", "صغير": "fsize:<100kb", "صغيره": "fsize:<100kb",
	"لقطه": "screenshot:true", "لقطات": "screenshot:true",
}

func init() {
	folded := map[string]string{}
	for w, f := range filterWords {
		folded[fold.Fold(w)] = f
	}
	filterWords = folded
}

// phraseFilter: two words that name a period ("last week", "this month",
// "الاسبوع الماضي") as a filter, relative to now; "" when they do not.
func phraseFilter(first, second string, now int64) string {
	a, b := fold.Fold(first), fold.Fold(second)
	periods := map[string]string{"day": "1d", "week": "7d", "month": "30d", "year": "365d"}
	switch a {
	case "last", "past":
		if d, ok := periods[b]; ok {
			return "since:" + d
		}
	case "this":
		t := time.Unix(now, 0)
		switch b {
		case "week": // since Monday
			return fmt.Sprintf("since:%dd", (int(t.Weekday())+6)%7+1)
		case "month":
			return t.Format("modified:2006-01")
		case "year":
			return t.Format("modified:2006")
		}
	}
	// Arabic puts the noun first: الاسبوع الماضي = last week.
	if b == fold.Fold("الماضي") || b == fold.Fold("الماضية") {
		switch a {
		case fold.Fold("الاسبوع"):
			return "since:7d"
		case fold.Fold("الشهر"):
			return "since:30d"
		case fold.Fold("السنة"), fold.Fold("العام"):
			return "since:365d"
		}
	}
	return ""
}

// wordFilter parses the filter a plain-language word stands for.
func (p *parser) wordFilter(text string, span Span) (*Filter, bool) {
	f, err := p.maybeFilter([]rune(text), span.Start, NoScope)
	if err != nil || f == nil {
		return nil, false
	}
	f.Span = span
	return f, true
}

// WordMeaning: what a plain-language word, or the phrase ending in it
// (prev + word), stands for — for the hint line; "" when nothing.
func WordMeaning(prev, word string) string {
	if m := TypeWordMeaning(word); m != "" {
		return m
	}
	if f, ok := filterWords[fold.Fold(word)]; ok {
		return f
	}
	if prev != "" {
		return phraseFilter(prev, word, time.Now().Unix())
	}
	return ""
}
