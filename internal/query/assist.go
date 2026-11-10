package query

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Typing assistance (spec §8.1–8.2, §8.9.6): the hint line, the cheat-sheet
// section under the caret, and autocomplete. Offsets are rune offsets.

// Completion is one suggestion: replace runes [ReplaceStart, ReplaceEnd)
// with Insert.
type Completion struct {
	Label        string `json:"label"`
	Detail       string `json:"detail"`
	Insert       string `json:"insert"`
	ReplaceStart int    `json:"replace_start"`
	ReplaceEnd   int    `json:"replace_end"`
}

// FilterInfo describes a filter for the cheat sheet.
type FilterInfo struct {
	// Key as written in queries, with a scope prefix where the example uses
	// one (s.lang).
	Key string `json:"key"`
	// Section: the title in the requested language; SectionID is stable.
	Section     string   `json:"section"`
	SectionID   string   `json:"section_id"`
	Description string   `json:"description"`
	Example     string   `json:"example"`
	Values      []string `json:"values"`
}

// tokenAt is the whitespace-delimited token around cursor: (start, text).
func tokenAt(q string, cursor int) (int, string) {
	r := []rune(q)
	cursor = max(0, min(cursor, len(r)))
	start, end := cursor, cursor
	for start > 0 && !unicode.IsSpace(r[start-1]) {
		start--
	}
	for end < len(r) && !unicode.IsSpace(r[end]) {
		end++
	}
	return start, string(r[start:end])
}

// splitFilter splits -a.key:value into (scope prefix with dot, key, value,
// has colon).
func splitFilter(tok string) (scope, key, value string, hasValue bool) {
	tok = strings.TrimPrefix(tok, "-")
	head := tok
	if h, v, ok := strings.Cut(tok, ":"); ok {
		head, value, hasValue = h, v, true
	}
	if p, k, ok := strings.Cut(head, "."); ok && len(p) == 1 {
		return tok[:2], k, value, hasValue
	}
	return "", head, value, hasValue
}

func valuesHint(key string, lang Lang) (string, bool) {
	f := Lookup(key, NoScope)
	if f == nil {
		return "", false
	}
	d := Describe(f, lang)
	switch f.Value.Kind {
	case VEnum:
		return fmt.Sprintf("%s: %s — %s", key, strings.Join(f.Value.Values, " "), d), true
	case VSize:
		return fmt.Sprintf("%s: %s — %s", key, phrase(phSizes, lang), d), true
	case VDate:
		return fmt.Sprintf("%s: %s — %s", key, phrase(phDates, lang), d), true
	case VDuration:
		return fmt.Sprintf("%s: %s — %s", key, phrase(phDurations, lang), d), true
	case VBitrate:
		return fmt.Sprintf("%s: %s — %s", key, phrase(phBitrates, lang), d), true
	case VBool:
		return fmt.Sprintf("%s: %s — %s", key, phrase(phTrueFalse, lang), d), true
	case VNumber:
		return fmt.Sprintf("%s: %s %s", key, phrase(phNumber, lang), f.Example), true
	}
	return fmt.Sprintf("%s — %s %s", d, phrase(phExample, lang), f.Example), true
}

func errText(err error) string {
	var e *Error
	if errors.As(err, &e) && e.Suggestion != "" {
		return e.Message + " — try `" + e.Suggestion + "`"
	}
	return err.Error()
}

// Hint is the hint line under the query box with the caret at cursor
// ("" for none). Query errors are in English.
func Hint(q string, cursor int, lang Lang) string {
	if strings.TrimSpace(q) == "" {
		return phrase(phEmpty, lang)
	}
	if strings.HasPrefix(strings.TrimLeft(q, " \t"), "re:") {
		pq, err := Parse(q)
		if err == nil {
			_, err = Compile(pq, false)
		}
		if err != nil {
			return errText(err)
		}
		return phrase(phRawOK, lang)
	}
	_, tok := tokenAt(q, cursor)
	_, key, _, hasValue := splitFilter(tok)
	if hasValue {
		if h, ok := valuesHint(strings.ToLower(key), lang); ok {
			return h
		}
	}
	if _, err := Parse(q); err != nil {
		return errText(err)
	}
	if key != "" && !hasValue {
		prefix := strings.ToLower(key)
		var keys []string
		for i := range Filters {
			k := Filters[i].Key
			if strings.HasPrefix(k, prefix) && k != prefix && len(keys) < 5 {
				keys = append(keys, k+":")
			}
		}
		if len(keys) > 0 && len(prefix) >= 2 {
			return phrase(phFilters, lang) + " " + strings.Join(keys, " ")
		}
	}
	if strings.ContainsAny(q, "*?") {
		return phrase(phWildcards, lang)
	}
	if !strings.Contains(q, ":") {
		return phrase(phAddFilters, lang)
	}
	return ""
}

// SectionAt is the cheat-sheet section for the token under the caret: its
// key's, or the first filter its text is a prefix of (chan → Audio).
func SectionAt(q string, cursor int) (Section, bool) {
	_, tok := tokenAt(q, cursor)
	scope, key, _, _ := splitFilter(tok)
	key = strings.ToLower(key)
	if len(key) < 2 && scope == "" {
		return 0, false
	}
	if f := Lookup(key, NoScope); f != nil {
		return f.Section, true
	}
	if key == "" && scope != "" {
		switch scope[0] {
		case 'a':
			return SectionAudio, true
		case 'v':
			return SectionVideo, true
		case 's':
			return SectionSubtitles, true
		}
	}
	for i := range Filters {
		if strings.HasPrefix(Filters[i].Key, key) {
			return Filters[i].Section, true
		}
	}
	return 0, false
}

// Completions for the token under cursor.
func Completions(q string, cursor int, lang Lang) []Completion {
	start, tok := tokenAt(q, cursor)
	end := start + len([]rune(tok))
	neg := ""
	if strings.HasPrefix(tok, "-") {
		neg = "-"
	}
	scope, key, value, hasValue := splitFilter(tok)
	out := []Completion{}
	switch {
	case !hasValue && key != "":
		prefix := strings.ToLower(key)
		for i := range Filters {
			f := &Filters[i]
			if strings.HasPrefix(f.Key, prefix) {
				out = append(out, Completion{Label: scope + f.Key + ":", Detail: Describe(f, lang),
					Insert: neg + scope + f.Key + ":", ReplaceStart: start, ReplaceEnd: end})
			}
		}
	case hasValue:
		f := Lookup(strings.ToLower(key), NoScope)
		if f == nil {
			return out
		}
		base := neg + scope + f.Key + ":"
		i := strings.LastIndexByte(value, ',')
		typed, done := strings.ToLower(value[i+1:]), value[:i+1]
		var options []string
		switch f.Value.Kind {
		case VEnum:
			options = f.Value.Values
		case VBool:
			options = []string{"true", "false"}
		}
		for _, o := range options {
			if strings.HasPrefix(o, typed) {
				out = append(out, Completion{Label: o, Detail: Describe(f, lang), Insert: base + done + o, ReplaceStart: start, ReplaceEnd: end})
			}
		}
	}
	return out
}

// FilterList is every filter, for the cheat sheet.
func FilterList(lang Lang) []FilterInfo {
	var out []FilterInfo
	for i := range Filters {
		f := &Filters[i]
		key := f.Key
		if p, _, ok := strings.Cut(f.Example, "."); ok && len(p) == 1 {
			key = p + "." + f.Key
		}
		values := []string{}
		switch f.Value.Kind {
		case VEnum:
			values = f.Value.Values
		case VBool:
			values = []string{"true", "false"}
		}
		out = append(out, FilterInfo{Key: key, Section: SectionTitle(f.Section, lang), SectionID: f.Section.Title(),
			Description: Describe(f, lang), Example: f.Example, Values: values})
	}
	return out
}
