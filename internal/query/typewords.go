package query

import (
	"fmt"
	"strconv"
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
// "الاسبوع الماضي", "march 2024", "last summer") as a filter, relative to
// now; "" when they do not.
func phraseFilter(first, second string, now int64) string {
	if f, n := datePhrase([]string{first, second}, now); n == 2 {
		return f
	}
	return periodPhrase(first, second, now)
}

// PhraseAt: the longest date phrase starting at words[0] (up to 5 words:
// "between march and may 2024"), as a filter, and how many words it takes;
// 0 when none.
func PhraseAt(words []string, now int64) (string, int) {
	if f, n := datePhrase(words, now); n > 0 {
		return f, n
	}
	if len(words) >= 2 {
		if f := periodPhrase(words[0], words[1], now); f != "" {
			return f, 2
		}
	}
	return "", 0
}

var monthWords = map[string]int{}
var seasonWords = map[string]int{} // → the season's first month (winter: 12)

func init() {
	for i, names := range [][]string{
		{"january", "jan", "يناير"}, {"february", "feb", "فبراير"}, {"march", "mar", "مارس"},
		{"april", "apr", "ابريل", "أبريل", "إبريل"}, {"may", "مايو"}, {"june", "jun", "يونيو", "يونيه"},
		{"july", "jul", "يوليو", "يوليه"}, {"august", "aug", "اغسطس", "أغسطس"},
		{"september", "sep", "sept", "سبتمبر"}, {"october", "oct", "اكتوبر", "أكتوبر"},
		{"november", "nov", "نوفمبر"}, {"december", "dec", "ديسمبر"},
	} {
		for _, n := range names {
			monthWords[fold.Fold(n)] = i + 1
		}
	}
	for first, names := range map[int][]string{
		3: {"spring", "الربيع", "ربيع"}, 6: {"summer", "الصيف", "صيف"},
		9: {"autumn", "fall", "الخريف", "خريف"}, 12: {"winter", "الشتاء", "شتاء"},
	} {
		for _, n := range names {
			seasonWords[fold.Fold(n)] = first
		}
	}
}

// month: a month name, also with the Arabic "and" joined to it (ومارس).
func month(w string) (int, bool) {
	w = fold.Fold(w)
	if m, ok := monthWords[w]; ok {
		return m, true
	}
	if rest, ok := strings.CutPrefix(w, fold.Fold("و")); ok {
		m, ok := monthWords[rest]
		return m, ok
	}
	return 0, false
}

func year(w string) (int, bool) {
	if len(w) != 4 {
		return 0, false
	}
	y, err := strconv.Atoi(w)
	return y, err == nil && y >= 1900 && y <= 2200
}

// monthsFilter: modified from the start of (y1, m1) to the end of (y2, m2).
func monthsFilter(y1, m1, y2, m2 int) string {
	if y1 == y2 && m1 == m2 {
		return fmt.Sprintf("modified:%04d-%02d", y1, m1)
	}
	return fmt.Sprintf("modified:%04d-%02d..%04d-%02d", y1, m1, y2, m2)
}

// seasonFilter: the three months of a season starting in month first of
// year y (winter starts in December of the year before y's January).
func seasonFilter(y, first int) string {
	if first == 12 {
		return monthsFilter(y-1, 12, y, 2)
	}
	return monthsFilter(y, first, y, first+2)
}

// datePhrase: "march 2024", "مارس 2024", "summer 2023", "last summer",
// "this winter", "الصيف الماضي", "between march and may [2024]",
// "from jan to mar", "بين يناير ومارس", "من يناير الى مارس". A month alone
// is not one ("invoice march" is a name).
func datePhrase(words []string, now int64) (string, int) {
	if len(words) < 2 {
		return "", 0
	}
	t := time.Unix(now, 0)
	cy, cm := t.Year(), int(t.Month())
	w0, w1 := fold.Fold(words[0]), fold.Fold(words[1])
	// The season of year y that has begun by now, or the one before.
	lastSeason := func(first int) string {
		y := cy
		if first == 12 {
			// Winter y is December y-1 to February y; the last one over.
			if cm <= 2 {
				y = cy - 1
			}
			return seasonFilter(y, 12)
		}
		if cm < first+3 {
			y-- // this year's has not finished
		}
		return seasonFilter(y, first)
	}
	thisSeason := func(first int) string {
		if first == 12 {
			if cm == 12 {
				return seasonFilter(cy+1, 12)
			}
			return seasonFilter(cy, 12)
		}
		return seasonFilter(cy, first)
	}
	// Between two months: this year's span if it has begun, else last year's.
	span := func(m1, m2 int, y int, hasYear bool) string {
		y1 := y
		if !hasYear {
			y1 = cy
			if m1 > cm {
				y1--
			}
		}
		y2 := y1
		if m2 < m1 {
			if hasYear {
				y1-- // "between nov and feb 2025": Nov 2024 to Feb 2025
			} else {
				y2++
			}
		}
		return monthsFilter(y1, m1, y2, m2)
	}
	if m, ok := month(w0); ok {
		if y, ok := year(w1); ok {
			return monthsFilter(y, m, y, m), 2
		}
	}
	if first, ok := seasonWords[w0]; ok {
		if y, ok := year(w1); ok {
			return seasonFilter(y, first), 2
		}
		if w1 == fold.Fold("الماضي") || w1 == fold.Fold("الماضية") {
			return lastSeason(first), 2
		}
		if w1 == fold.Fold("هذا") || w1 == fold.Fold("الحالي") {
			return thisSeason(first), 2
		}
	}
	if first, ok := seasonWords[w1]; ok {
		switch w0 {
		case "last", "past":
			return lastSeason(first), 2
		case "this":
			return thisSeason(first), 2
		}
		if w0 == fold.Fold("هذا") {
			return thisSeason(first), 2
		}
	}
	// between / from M1 and / to M2 [year]; Arabic "و" may join M2.
	isStart := w0 == "between" || w0 == "from" || w0 == fold.Fold("بين") || w0 == fold.Fold("من")
	if isStart && len(words) >= 3 {
		m1, ok := month(words[1])
		if !ok {
			return "", 0
		}
		i := 2
		if c := fold.Fold(words[2]); c == "and" || c == "to" || c == "till" || c == "until" || c == "-" ||
			c == fold.Fold("و") || c == fold.Fold("الى") || c == fold.Fold("حتى") {
			i = 3
		}
		if i >= len(words) {
			return "", 0
		}
		m2, ok := month(words[i])
		if !ok || (i == 2 && !strings.HasPrefix(fold.Fold(words[2]), fold.Fold("و"))) {
			return "", 0
		}
		if i+1 < len(words) {
			if y, ok := year(words[i+1]); ok {
				return span(m1, m2, y, true), i + 2
			}
		}
		return span(m1, m2, 0, false), i + 1
	}
	return "", 0
}

// periodPhrase: "last week", "this month", "الاسبوع الماضي".
func periodPhrase(first, second string, now int64) string {
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
