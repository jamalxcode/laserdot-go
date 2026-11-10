package media

import "strings"

// Language codes: tracks store ISO 639-1 ("ar") where one exists; every
// alias ("ar", "ara") matches in queries.

// langs: 639-1, 639-2/T, 639-2/B if different.
var langs = [][3]string{
	{"ar", "ara", ""},
	{"en", "eng", ""},
	{"fr", "fra", "fre"},
	{"de", "deu", "ger"},
	{"es", "spa", ""},
	{"it", "ita", ""},
	{"pt", "por", ""},
	{"ru", "rus", ""},
	{"zh", "zho", "chi"},
	{"ja", "jpn", ""},
	{"ko", "kor", ""},
	{"hi", "hin", ""},
	{"ur", "urd", ""},
	{"fa", "fas", "per"},
	{"tr", "tur", ""},
	{"he", "heb", ""},
	{"nl", "nld", "dut"},
	{"sv", "swe", ""},
	{"no", "nor", ""},
	{"nb", "nob", ""},
	{"da", "dan", ""},
	{"fi", "fin", ""},
	{"pl", "pol", ""},
	{"cs", "ces", "cze"},
	{"el", "ell", "gre"},
	{"hu", "hun", ""},
	{"ro", "ron", "rum"},
	{"th", "tha", ""},
	{"vi", "vie", ""},
	{"id", "ind", ""},
	{"ms", "msa", "may"},
	{"uk", "ukr", ""},
	{"bg", "bul", ""},
	{"hr", "hrv", ""},
	{"sr", "srp", ""},
	{"sk", "slk", "slo"},
	{"sl", "slv", ""},
	{"ta", "tam", ""},
	{"te", "tel", ""},
	{"bn", "ben", ""},
	{"ku", "kur", ""},
	{"ca", "cat", ""},
	{"eu", "eus", "baq"},
	{"gl", "glg", ""},
	{"is", "isl", "ice"},
	{"lt", "lit", ""},
	{"lv", "lav", ""},
	{"et", "est", ""},
	{"sw", "swa", ""},
	{"tl", "tgl", ""},
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func asciiUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

// NormalizeLang is the canonical code for a stored or typed language tag:
// lowercase, region stripped ("en-US" → "en"), 3-letter codes mapped to
// 2-letter when known.
func NormalizeLang(tag string) string {
	base := tag
	if i := strings.IndexAny(base, "-_"); i >= 0 {
		base = base[:i]
	}
	base = asciiLower(strings.TrimSpace(base))
	for _, l := range langs {
		if base == l[0] || base == l[1] || (l[2] != "" && base == l[2]) {
			return l[0]
		}
	}
	return base
}

// LangAliases are every alias of a canonical code, for query matching (nil
// when the code is unknown).
func LangAliases(code string) []string {
	for _, l := range langs {
		if l[0] == code {
			out := []string{l[0], l[1]}
			if l[2] != "" {
				out = append(out, l[2])
			}
			return out
		}
	}
	return nil
}
