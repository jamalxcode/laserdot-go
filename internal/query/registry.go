// Package query is the query language (spec §2.7): the filter registry
// (§8.8), the parser, and compilation to regexes and filters.
package query

import "strings"

// Scope is the track a filter applies to (spec §2.7 "Track scope").
type Scope int

const (
	NoScope Scope = iota
	Video
	Audio
	Subtitle
)

// ScopeFromPrefix maps v/a/s to a scope.
func ScopeFromPrefix(c rune) (Scope, bool) {
	switch c {
	case 'v':
		return Video, true
	case 'a':
		return Audio, true
	case 's':
		return Subtitle, true
	}
	return NoScope, false
}

// Prefix is the scope's one-letter prefix.
func (s Scope) Prefix() string {
	switch s {
	case Video:
		return "v"
	case Audio:
		return "a"
	case Subtitle:
		return "s"
	}
	return ""
}

func (s Scope) name() string {
	switch s {
	case Video:
		return "video"
	case Audio:
		return "audio"
	case Subtitle:
		return "subtitle"
	}
	return ""
}

// Section is a cheat-sheet section (spec §8.9.3).
type Section int

const (
	SectionFile Section = iota
	SectionVideo
	SectionAudio
	SectionSubtitles
	SectionImages
	SectionMusic
)

// Sections in cheat-sheet order.
var Sections = []Section{SectionFile, SectionVideo, SectionAudio, SectionSubtitles, SectionImages, SectionMusic}

func (s Section) Title() string {
	return [...]string{"File", "Video", "Audio", "Subtitles", "Images", "Music"}[s]
}

// ValueKind decides how a filter's value text is parsed.
type ValueKind int

const (
	// VText: patterns and phrases; Exact means a plain word must equal the
	// value (ext:mkv) instead of matching a substring.
	VText     ValueKind = iota
	VPath               // a folder (in:)
	VSize               // bytes; b/kb/mb/gb/tb, decimal as Finder
	VDate               // YYYY, YYYY-MM, YYYY-MM-DD, today, yesterday, 7d/2w/3mo/1y
	VDuration           // seconds; s/min/h
	VBitrate            // bits per second; kbps/mbps
	VNumber
	VBool
	VEnum
)

// ValueType is a filter's value type.
type ValueType struct {
	Kind   ValueKind
	Exact  bool     // VText
	Values []string // VEnum
}

// FilterDef declares one filter.
type FilterDef struct {
	ID      FilterID
	Key     string
	Section Section
	Value   ValueType
	// Scopes allowed with x.key:; the first is the natural scope used when
	// the key has no prefix. Empty: not track-scoped.
	Scopes      []Scope
	Description string
	Example     string
}

// Kinds and Res are the values of kind: and res:.
var (
	Kinds = []string{"video", "audio", "image", "document", "archive", "code", "app", "folder", "other"}
	Res   = []string{"sd", "720p", "1080p", "1440p", "4k", "8k"}
)

// Lookup finds a filter by key; with a scope, only if the filter allows it.
func Lookup(key string, scope Scope) *FilterDef {
	for i := range Filters {
		f := &Filters[i]
		if f.Key != key {
			continue
		}
		if scope == NoScope {
			return f
		}
		for _, s := range f.Scopes {
			if s == scope {
				return f
			}
		}
	}
	return nil
}

// ByID finds a filter by id.
func ByID(id FilterID) *FilterDef {
	for i := range Filters {
		if Filters[i].ID == id {
			return &Filters[i]
		}
	}
	return nil
}

// SuggestKey is the closest filter key, if any is close enough.
func SuggestKey(key string) string {
	best, bestD := "", 3
	for _, f := range Filters {
		d := EditDistance(key, f.Key)
		if d <= 2 && d < len(f.Key) && d < bestD {
			best, bestD = f.Key, d
		}
	}
	return best
}

// SuggestValue is the closest of values, if any is close enough.
func SuggestValue(value string, values []string) string {
	best, bestD := "", 3
	for _, v := range values {
		if d := EditDistance(value, v); d <= 2 && d < bestD {
			best, bestD = v, d
		}
	}
	return best
}

// EditDistance is the Levenshtein distance in characters.
func EditDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

// scopedKey writes the key the way the example does (s.lang:).
func (f *FilterDef) scopedKey() string {
	if p, _, ok := strings.Cut(f.Example, "."); ok && len(p) == 1 {
		return p + "." + f.Key
	}
	return f.Key
}
