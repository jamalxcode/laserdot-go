package query

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jamalxcode/laserdot-go/internal/fold"
)

// Every name matcher is built as a regexp/syntax tree — never by splicing
// user text into a pattern — so user text is literal by construction (spec
// §2.2.5). Go compiles regexes from text only, so a tree is serialized with
// the library's own String (which escapes every literal) and compiled. Each
// word becomes its own regex; clauses are ANDed by the executor (§2.2.1).

// MatchQuality is how well a name matched, best first (relevance, §10.1).
type MatchQuality int

const (
	Exact MatchQuality = iota
	Prefix
	WordStart
	Substring
	Typo
	NoMatch
)

// TypoMinChars and TypoMaxChars bound the words that get typo alternatives
// (§2.2.3). Alternatives grow with the square of the length, so very long
// "words" (pasted text) are matched exactly.
const (
	TypoMinChars = 4
	TypoMaxChars = 40
)

// NameMatcher is a compiled regex over folded names (or folded paths).
type NameMatcher struct {
	Regex *regexp.Regexp
	// Prefilter for the trigram index (§2.1.1b): every match contains at
	// least one literal from each group (an AND of ORs). Empty: none.
	Prefilter [][]string
	// OnPath: match the full folded path instead of the name.
	OnPath bool
	// Typo: built with typo alternatives.
	Typo bool
}

func (m *NameMatcher) Match(s string) bool { return m.Regex.MatchString(s) }

// Quality of s, or NoMatch.
func (m *NameMatcher) Quality(s string) MatchQuality {
	loc := m.Regex.FindStringIndex(s)
	if loc == nil {
		return NoMatch
	}
	if m.Typo {
		return Typo
	}
	switch {
	case loc[0] == 0 && loc[1] == len(s):
		return Exact
	case loc[0] == 0:
		return Prefix
	}
	r, _ := utf8.DecodeLastRuneInString(s[:loc[0]])
	if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
		return WordStart
	}
	return Substring
}

// CValue is a compiled filter value.
type CValue struct {
	Text     []*NameMatcher // any of
	Path     string
	Interval Interval
	Bool     bool
	Enum     []string
}

// CFilter is a compiled filter.
type CFilter struct {
	ID    FilterID
	Scope Scope
	Value CValue
	// Span: position in the query, for execution-time errors (an in: folder
	// that is not indexed).
	Span Span
}

// CKind says what a compiled term is.
type CKind int

const (
	CName CKind = iota
	CFilterTerm
	CGroup // one track of Scope must match every filter
)

// CTerm is a compiled term.
type CTerm struct {
	Negated bool
	Kind    CKind
	Name    *NameMatcher
	Filter  CFilter
	Scope   Scope
	Group   []CFilter
	Span    Span
}

// CClause is compiled terms joined by OR.
type CClause struct{ Terms []CTerm }

// Compiled is an executable query: every clause must match.
type Compiled struct{ Clauses []CClause }

// TrackFilters can go inside a track group.
var TrackFilters = map[FilterID]bool{
	FLang: true, FChannels: true, FAcodec: true, FCodec: true, FProfile: true, FPixFmt: true,
	FBitDepth: true, FHdr: true, FFps: true, FAtmos: true, FLossless: true, FSampleRate: true,
	FForced: true, FSubFormat: true,
}

// Compile compiles q. With typo, eligible words (no wildcards, not negated,
// not a path, at least TypoMinChars, no digits) get single-edit
// alternatives.
func Compile(q *Query, typo bool) (*Compiled, error) {
	c := &Compiled{}
	for _, clause := range q.Clauses {
		var terms []CTerm
		for _, term := range clause.Terms {
			ct := CTerm{Negated: term.Negated, Span: term.Span}
			switch term.Kind {
			case TermText:
				m, err := textMatcher(term.Text, false, typo && !term.Negated, term.Span)
				if err != nil {
					return nil, err
				}
				ct.Kind, ct.Name = CName, m
			case TermFilter:
				f := term.Filter
				v, err := value(f.Value, f.Def.Value, f.Span)
				if err != nil {
					return nil, err
				}
				ct.Kind = CFilterTerm
				ct.Filter = CFilter{ID: f.Def.ID, Scope: f.Scope, Value: v, Span: f.Span}
			case TermGroup:
				ct.Kind, ct.Scope = CGroup, term.Scope
				for _, f := range term.Group {
					if !TrackFilters[f.Def.ID] {
						return nil, newErr(f.Span, "`%s:` describes the whole file, so it can't go inside a track group", f.Def.Key)
					}
					v, err := value(f.Value, f.Def.Value, f.Span)
					if err != nil {
						return nil, err
					}
					ct.Group = append(ct.Group, CFilter{ID: f.Def.ID, Scope: term.Scope, Value: v, Span: f.Span})
				}
			}
			terms = append(terms, ct)
		}
		c.Clauses = append(c.Clauses, CClause{Terms: terms})
	}
	if q.Raw != nil {
		m, err := rawMatcher(q.Raw)
		if err != nil {
			return nil, err
		}
		c.Clauses = append(c.Clauses, CClause{Terms: []CTerm{{Kind: CName, Name: m, Span: q.Raw.Span}}})
	}
	return c, nil
}

// HasTypoCandidates: compiling with typo alternatives would change anything.
func HasTypoCandidates(q *Query) bool {
	for _, c := range q.Clauses {
		for _, t := range c.Terms {
			if !t.Negated && t.Kind == TermText && !t.Text.Phrase && !t.Text.Wildcards && typoEligible(t.Text.Text) {
				return true
			}
		}
	}
	return false
}

func value(v FilterValue, ty ValueType, span Span) (CValue, error) {
	out := CValue{Path: v.Path, Interval: v.Interval, Bool: v.Bool, Enum: v.Enum}
	for _, t := range v.Text {
		m, err := textMatcher(t, ty.Kind == VText && ty.Exact, false, span)
		if err != nil {
			return CValue{}, err
		}
		out.Text = append(out.Text, m)
	}
	return out, nil
}

// typoEligible: long enough, not a path, no digits (2024 must not match
// 2023).
func typoEligible(text string) bool {
	if n := utf8.RuneCountInString(text); n < TypoMinChars || n > TypoMaxChars || strings.Contains(text, "/") {
		return false
	}
	for _, c := range text {
		if unicode.IsNumber(c) {
			return false
		}
	}
	return true
}

func textMatcher(tv TextValue, exact, typo bool, span Span) (*NameMatcher, error) {
	var (
		re        *syntax.Regexp
		prefilter [][]string
		onPath    = strings.Contains(tv.Text, "/")
		isTypo    bool
	)
	switch {
	case tv.Phrase:
		re = lit(tv.Text)
		if exact {
			re = anchored(re)
		}
		prefilter = [][]string{{tv.Text}}
	case tv.Wildcards:
		re, prefilter = glob(tv.Text)
	case exact:
		re, prefilter = anchored(lit(tv.Text)), [][]string{{tv.Text}}
	case typo && typoEligible(tv.Text):
		var pieces []string
		re, pieces = typoTree(tv.Text)
		prefilter, onPath, isTypo = [][]string{pieces}, false, true
	default:
		re, prefilter = lit(tv.Text), [][]string{{tv.Text}}
	}
	rx, err := build(re, span)
	if err != nil {
		return nil, err
	}
	return &NameMatcher{Regex: rx, Prefilter: prefilter, OnPath: onPath, Typo: isTypo}, nil
}

// glob: a whole-name match (§2.2.2): ^ segments joined by .* and . $.
func glob(text string) (*syntax.Regexp, [][]string) {
	var parts []*syntax.Regexp
	var literals [][]string
	var seg strings.Builder
	flush := func() {
		if seg.Len() > 0 {
			parts = append(parts, lit(seg.String()))
			literals = append(literals, []string{seg.String()})
			seg.Reset()
		}
	}
	for _, c := range text {
		switch c {
		case '*':
			flush()
			parts = append(parts, &syntax.Regexp{Op: syntax.OpStar, Sub: []*syntax.Regexp{anyChar()}})
		case '?':
			flush()
			parts = append(parts, anyChar())
		default:
			seg.WriteRune(c)
		}
	}
	flush()
	return anchored(parts...), literals
}

// typoTree is the word plus every single-edit variant: substitution,
// deletion, insertion and adjacent transposition (§2.2.3). It also returns,
// per variant, its longest literal piece — every match contains one, which
// gives the trigram prefilter an any-of group.
func typoTree(word string) (*syntax.Regexp, []string) {
	c := []rune(word)
	n := len(c)
	s := func(r []rune) string { return string(r) }
	alts := []*syntax.Regexp{lit(word)}
	pieces := []string{word}
	gapped := func(before, after string) {
		if len(before) >= len(after) {
			pieces = append(pieces, before)
		} else {
			pieces = append(pieces, after)
		}
		alts = append(alts, concat(lit(before), anyChar(), lit(after)))
	}
	for i := 0; i < n; i++ {
		gapped(s(c[:i]), s(c[i+1:])) // substitution
		del := s(c[:i]) + s(c[i+1:]) // deletion
		pieces = append(pieces, del)
		alts = append(alts, lit(del))
		if i > 0 { // insertion (at the ends, substring matching implies it)
			gapped(s(c[:i]), s(c[i:]))
		}
		if i+1 < n && c[i] != c[i+1] { // transposition
			t := append([]rune(nil), c...)
			t[i], t[i+1] = t[i+1], t[i]
			pieces = append(pieces, s(t))
			alts = append(alts, lit(s(t)))
		}
	}
	sort.Strings(pieces)
	pieces = dedup(pieces)
	return &syntax.Regexp{Op: syntax.OpAlternate, Sub: alts}, pieces
}

func rawMatcher(raw *Raw) (*NameMatcher, error) {
	re, err := syntax.Parse(raw.Regex, syntax.Perl|syntax.FoldCase)
	if err != nil {
		return nil, rawError(err, raw)
	}
	rx, cerr := build(re, raw.Span)
	if cerr != nil {
		return nil, cerr
	}
	return &NameMatcher{Regex: rx, Prefilter: rawPrefilter(re)}, nil
}

// rawPrefilter: literals every match must contain — those of the top-level
// sequence (through capture groups), each its own group, and alternations of
// plain literals as one any-of group. Names are folded, so literals are too.
func rawPrefilter(re *syntax.Regexp) [][]string {
	var groups [][]string
	var walk func(r *syntax.Regexp)
	walk = func(r *syntax.Regexp) {
		switch r.Op {
		case syntax.OpConcat:
			for _, s := range r.Sub {
				walk(s)
			}
		case syntax.OpCapture:
			walk(r.Sub[0])
		case syntax.OpLiteral:
			groups = append(groups, []string{fold.Fold(string(r.Rune))})
		case syntax.OpAlternate:
			var g []string
			for _, s := range r.Sub {
				if s.Op != syntax.OpLiteral {
					return
				}
				g = append(g, fold.Fold(string(s.Rune)))
			}
			sort.Strings(g)
			groups = append(groups, dedup(g))
		}
	}
	walk(re.Simplify())
	return groups
}

func lit(s string) *syntax.Regexp {
	if s == "" {
		return &syntax.Regexp{Op: syntax.OpEmptyMatch}
	}
	return &syntax.Regexp{Op: syntax.OpLiteral, Rune: []rune(s)}
}

func anyChar() *syntax.Regexp { return &syntax.Regexp{Op: syntax.OpAnyChar} }

func concat(parts ...*syntax.Regexp) *syntax.Regexp {
	return &syntax.Regexp{Op: syntax.OpConcat, Sub: parts}
}

func anchored(parts ...*syntax.Regexp) *syntax.Regexp {
	all := append([]*syntax.Regexp{{Op: syntax.OpBeginText}}, parts...)
	return concat(append(all, &syntax.Regexp{Op: syntax.OpEndText})...)
}

func build(re *syntax.Regexp, span Span) (*regexp.Regexp, *Error) {
	rx, err := regexp.Compile(re.Simplify().String())
	if err != nil {
		return nil, newErr(span, "pattern is too complex: %v", err)
	}
	return rx, nil
}

// rawError positions a regex syntax error: Go reports the offending
// expression, which is located inside the regex as typed.
func rawError(err error, raw *Raw) *Error {
	msg, at, width := err.Error(), 0, utf8.RuneCountInString(raw.Regex)
	var se *syntax.Error
	if errors.As(err, &se) {
		msg = se.Code.String()
		if i := strings.Index(raw.Regex, se.Expr); se.Expr != "" && i >= 0 {
			at = utf8.RuneCountInString(raw.Regex[:i])
			width = max(1, utf8.RuneCountInString(se.Expr))
		}
	}
	start := raw.Span.Start + at
	end := min(start+width, raw.Span.End)
	return &Error{Message: fmt.Sprintf("invalid regex: %s", msg), Span: Span{start, max(end, start)}}
}

func dedup(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
