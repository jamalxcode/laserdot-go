package query

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jamalxcode/laserdot-go/internal/fold"
)

// Span is a half-open range of rune offsets into the query as typed, so the
// UI can underline errors correctly in any script.
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Error is a parse or compile error, positioned for the hint line (§8.1).
type Error struct {
	Message    string `json:"message"`
	Span       Span   `json:"span"`
	Suggestion string `json:"suggestion,omitempty"`
}

func (e *Error) Error() string {
	s := fmt.Sprintf("%s (at %d..%d)", e.Message, e.Span.Start, e.Span.End)
	if e.Suggestion != "" {
		s += fmt.Sprintf(" — did you mean `%s`?", e.Suggestion)
	}
	return s
}

func newErr(span Span, format string, args ...any) *Error {
	return &Error{Message: fmt.Sprintf(format, args...), Span: span}
}

func (e *Error) suggest(s string) *Error {
	e.Suggestion = s
	return e
}

// Warning is a non-fatal note, e.g. an unknown filter key searched as text.
type Warning struct {
	Message    string `json:"message"`
	Span       Span   `json:"span"`
	Suggestion string `json:"suggestion,omitempty"`
}

// Interval is the half-open numeric interval [Lo, Hi): sizes in bytes,
// dates in Unix seconds (UTC), durations in seconds.
type Interval struct{ Lo, Hi float64 }

// All is the unbounded interval.
var All = Interval{math.Inf(-1), math.Inf(1)}

func (i Interval) Contains(x float64) bool { return x >= i.Lo && x < i.Hi }

// Pattern is a word, already folded, possibly with * and ? wildcards.
type Pattern struct {
	Text      string
	Wildcards bool
}

// TextValue is a pattern or (when Phrase) an exact folded phrase, whose
// wildcards are literal.
type TextValue struct {
	Pattern
	Phrase bool
}

// FilterValue is a parsed filter value; exactly one field is meaningful,
// depending on the filter's value kind.
type FilterValue struct {
	Text     []TextValue // any of (a list is an OR)
	Path     string
	Interval Interval
	Bool     bool
	Enum     []string // any of
}

// Filter is key:value with its definition and track scope.
type Filter struct {
	Def   *FilterDef
	Scope Scope
	Value FilterValue
	Span  Span
}

// TermKind says what a term is.
type TermKind int

const (
	TermText TermKind = iota
	TermFilter
	// TermGroup: a(lang:ar channels:5.1) — one track must match every filter.
	TermGroup
)

// Term is one term of a clause.
type Term struct {
	Negated bool
	Span    Span
	Kind    TermKind
	Text    TextValue // TermText
	Filter  Filter    // TermFilter
	Scope   Scope     // TermGroup
	Group   []Filter  // TermGroup
}

// Clause is terms joined by OR.
type Clause struct{ Terms []Term }

// Raw is a re: regex, passed to the regex engine untouched.
type Raw struct {
	Regex string
	Span  Span
}

// Query is a parsed query: clauses are ANDed.
type Query struct {
	Clauses  []Clause
	Raw      *Raw
	Warnings []Warning
}

// IsEmpty: no clauses and no regex.
func (q *Query) IsEmpty() bool { return len(q.Clauses) == 0 && q.Raw == nil }

// Parse parses a query, using the current time for relative dates.
func Parse(input string) (*Query, error) {
	return ParseAt(input, time.Now().Unix())
}

// ParseAt parses with an explicit "now" (Unix seconds).
func ParseAt(input string, now int64) (*Query, error) {
	chars := []rune(fold.PrepareQuery(input))
	tokens, raw, err := tokenize(chars, 0, len(chars), true)
	if err != nil {
		return nil, err
	}
	p := &parser{now: now}
	q := &Query{Raw: raw}
	var pendingOr *Span
	for _, tok := range tokens {
		if tok.text == "OR" {
			if len(q.Clauses) == 0 || pendingOr != nil {
				return nil, newErr(tok.span, "OR needs a term on both sides")
			}
			s := tok.span
			pendingOr = &s
			continue
		}
		term, err := p.term(tok)
		if err != nil {
			return nil, err
		}
		if pendingOr != nil {
			last := &q.Clauses[len(q.Clauses)-1]
			last.Terms = append(last.Terms, term)
			pendingOr = nil
		} else {
			q.Clauses = append(q.Clauses, Clause{Terms: []Term{term}})
		}
	}
	if pendingOr != nil {
		return nil, newErr(*pendingOr, "OR needs a term on both sides")
	}
	q.Warnings = p.warnings
	return q, nil
}

type token struct {
	text string
	span Span
}

// tokenize splits chars[from:to] on whitespace, keeping quoted text and
// track groups together. With allowRaw, re: takes the rest.
func tokenize(chars []rune, from, to int, allowRaw bool) ([]token, *Raw, *Error) {
	var tokens []token
	i := from
	for i < to {
		if unicode.IsSpace(chars[i]) {
			i++
			continue
		}
		if allowRaw && to-i >= 3 && chars[i] == 'r' && chars[i+1] == 'e' && chars[i+2] == ':' {
			regex := strings.TrimRightFunc(string(chars[i+3:to]), unicode.IsSpace)
			if regex == "" {
				return nil, nil, newErr(Span{i, to}, "re: needs a regex after it")
			}
			return tokens, &Raw{Regex: regex, Span: Span{i + 3, to}}, nil
		}
		start := i
		quoteStart, groupOpen := -1, -1
		for i < to {
			c := chars[i]
			if quoteStart >= 0 {
				if c == '\\' && i+1 < to {
					i += 2
					continue
				}
				if c == '"' {
					quoteStart = -1
				}
				i++
				continue
			}
			switch {
			case c == '"':
				quoteStart = i
			case c == '(' && groupOpen < 0 && isGroupPrefix(chars[start:i]):
				groupOpen = i
			case c == ')' && groupOpen >= 0:
				groupOpen = -1
			case unicode.IsSpace(c) && groupOpen < 0:
				goto done
			}
			i++
		}
	done:
		if quoteStart >= 0 {
			return nil, nil, newErr(Span{quoteStart, to}, "unclosed quote").suggest(`add a closing "`)
		}
		if groupOpen >= 0 {
			return nil, nil, newErr(Span{groupOpen, to}, "unclosed track group").suggest("add a closing )")
		}
		tokens = append(tokens, token{text: string(chars[start:i]), span: Span{start, i}})
	}
	return tokens, nil, nil
}

func isGroupPrefix(before []rune) bool {
	if len(before) > 0 && before[0] == '-' {
		before = before[1:]
	}
	if len(before) != 1 {
		return false
	}
	_, ok := ScopeFromPrefix(before[0])
	return ok
}

type parser struct {
	now      int64
	warnings []Warning
}

func (p *parser) term(tok token) (Term, error) {
	chars := []rune(tok.text)
	negated, body, offset := false, chars, tok.span.Start
	if len(chars) > 1 && chars[0] == '-' {
		negated, body, offset = true, chars[1:], tok.span.Start+1
	}
	span := tok.span
	// Track group: a(...)
	if len(body) >= 3 && body[1] == '(' && body[len(body)-1] == ')' {
		if scope, ok := ScopeFromPrefix(body[0]); ok {
			filters, err := p.group(scope, body, offset)
			if err != nil {
				return Term{}, err
			}
			return Term{Negated: negated, Span: span, Kind: TermGroup, Scope: scope, Group: filters}, nil
		}
	}
	// Whole-token phrase: "march invoice"
	if len(body) >= 2 && body[0] == '"' && body[len(body)-1] == '"' {
		text := fold.Fold(unescape(body[1 : len(body)-1]))
		return Term{Negated: negated, Span: span, Kind: TermText, Text: TextValue{Pattern{Text: text}, true}}, nil
	}
	f, err := p.maybeFilter(body, offset, NoScope)
	if err != nil {
		return Term{}, err
	}
	if f != nil {
		return Term{Negated: negated, Span: span, Kind: TermFilter, Filter: *f}, nil
	}
	pat := pattern(string(body))
	if !pat.Wildcards {
		if tf, ok := p.typeFilter(pat.Text, span); ok {
			return Term{Negated: negated, Span: span, Kind: TermFilter, Filter: *tf}, nil
		}
	}
	return Term{Negated: negated, Span: span, Kind: TermText, Text: TextValue{Pattern: pat}}, nil
}

func (p *parser) group(scope Scope, body []rune, offset int) ([]Filter, error) {
	innerStart := offset + 2
	innerEnd := offset + len(body) - 1
	// Re-tokenise the inside with spans relative to the query.
	padded := make([]rune, innerStart, innerStart+len(body))
	for i := range padded {
		padded[i] = ' '
	}
	padded = append(padded, body[2:len(body)-1]...)
	tokens, _, err := tokenize(padded, innerStart, innerEnd, false)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, newErr(Span{offset, innerEnd + 1}, "empty track group")
	}
	var filters []Filter
	for _, t := range tokens {
		f, err := p.maybeFilter([]rune(t.text), t.span.Start, scope)
		if err != nil {
			return nil, err
		}
		if f == nil {
			return nil, newErr(t.span, "only filters can go inside %s( ), e.g. %s(lang:ar)", scope.Prefix(), scope.Prefix())
		}
		filters = append(filters, *f)
	}
	return filters, nil
}

// maybeFilter returns nil (no error) when the token is not a filter, so it
// is a word.
func (p *parser) maybeFilter(body []rune, offset int, groupScope Scope) (*Filter, error) {
	colon := -1
	for i, c := range body {
		if c == ':' {
			colon = i
			break
		}
	}
	if colon < 0 {
		return nil, nil
	}
	keyPart := string(body[:colon])
	scope, key := NoScope, keyPart
	if pre, k, ok := strings.Cut(keyPart, "."); ok && len([]rune(pre)) == 1 {
		s, ok := ScopeFromPrefix([]rune(pre)[0])
		if !ok {
			return nil, nil
		}
		scope, key = s, k
	}
	key = strings.ToLower(key) // ASCII after the identifier check below
	keySpan := Span{offset, offset + colon}
	if !isIdent(key) {
		return nil, nil // e.g. 10:30.mp4
	}
	if scope != NoScope && groupScope != NoScope {
		return nil, newErr(keySpan, "no scope prefix needed inside a track group")
	}
	if scope == NoScope {
		scope = groupScope
	}
	var def *FilterDef
	if Lookup(key, NoScope) == nil {
		if groupScope != NoScope {
			e := newErr(keySpan, "unknown filter `%s:`", key)
			if s := SuggestKey(key); s != "" {
				e.suggest(s + ":")
			}
			return nil, e
		}
		w := Warning{Message: fmt.Sprintf("`%s:` is not a filter, so it was searched as text", key), Span: keySpan}
		if s := SuggestKey(key); s != "" {
			w.Suggestion = s + ":"
		}
		p.warnings = append(p.warnings, w)
		return nil, nil
	}
	def = Lookup(key, scope)
	if def == nil {
		if groupScope != NoScope {
			return nil, newErr(keySpan, "`%s:` describes the whole file, so it can't go inside a track group", key)
		}
		return nil, newErr(keySpan, "`%s:` does not apply to %s tracks", key, scope.name())
	}
	valueSpan := Span{offset + colon + 1, offset + len(body)}
	value, err := p.value(def, body[colon+1:], valueSpan)
	if err != nil {
		return nil, err
	}
	return &Filter{Def: def, Scope: scope, Value: value, Span: Span{offset, offset + len(body)}}, nil
}

func isIdent(key string) bool {
	if len(key) < 2 {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c == '_') {
			return false
		}
	}
	return true
}

func quoted(r []rune) bool { return len(r) >= 2 && r[0] == '"' && r[len(r)-1] == '"' }

func (p *parser) value(def *FilterDef, raw []rune, span Span) (FilterValue, error) {
	text := string(raw)
	if text == "" {
		e := newErr(span, "`%s:` needs a value", def.Key)
		if def.Value.Kind == VEnum {
			e.suggest(def.Key + ":" + def.Value.Values[0])
		}
		return FilterValue{}, e
	}
	switch def.Value.Kind {
	case VText:
		var items []TextValue
		for _, item := range splitList(raw) {
			if len(item) == 0 {
				return FilterValue{}, newErr(span, "empty item in list")
			}
			if quoted(item) {
				items = append(items, TextValue{Pattern{Text: fold.Fold(unescape(item[1 : len(item)-1]))}, true})
			} else {
				items = append(items, TextValue{Pattern: pattern(string(item))})
			}
		}
		return FilterValue{Text: items}, nil
	case VPath:
		if quoted(raw) {
			return FilterValue{Path: unescape(raw[1 : len(raw)-1])}, nil
		}
		return FilterValue{Path: text}, nil
	case VBool:
		switch strings.ToLower(text) {
		case "true", "yes", "1":
			return FilterValue{Bool: true}, nil
		case "false", "no", "0":
			return FilterValue{Bool: false}, nil
		}
		return FilterValue{}, newErr(span, "`%s:` takes true or false", def.Key).suggest(def.Key + ":true")
	case VEnum:
		var out []string
		for _, item := range strings.Split(text, ",") {
			item = strings.ToLower(item)
			found := ""
			for _, v := range def.Value.Values {
				if v == item {
					found = v
				}
			}
			if found == "" {
				e := newErr(span, "`%s` is not a valid %s — use one of: %s", item, def.Key, strings.Join(def.Value.Values, " "))
				if s := SuggestValue(item, def.Value.Values); s != "" {
					e.suggest(def.Key + ":" + s)
				}
				return FilterValue{}, e
			}
			out = append(out, found)
		}
		return FilterValue{Enum: out}, nil
	}
	if (def.ID == FSince || def.ID == FBefore) && (strings.Contains(text, "..") || strings.ContainsAny(text[:1], "<>=")) {
		return FilterValue{}, newErr(span, "`%s:` takes a single date", def.Key).suggest("modified:" + text)
	}
	iv, err := p.interval(def, text, span)
	if err != nil {
		return FilterValue{}, err
	}
	return FilterValue{Interval: iv}, nil
}

func (p *parser) interval(def *FilterDef, text string, span Span) (Interval, error) {
	fail := func(msg string) error {
		hint := map[ValueKind]string{
			VSize:     "sizes look like 500mb, >4gb, 1gb..4gb",
			VDate:     "dates look like 2025, 2025-03, 2025-03-14, today, 7d",
			VDuration: "durations look like 90s, 45min, >2h",
			VBitrate:  "bit rates look like 800kbps, >20mbps",
		}[def.Value.Kind]
		if hint == "" {
			hint = "numbers look like 12, >=3840, 1..5"
		}
		return newErr(span, "%s — %s", msg, hint).suggest(def.Example)
	}
	if a, b, ok := strings.Cut(text, ".."); ok {
		lo, hi := math.Inf(-1), math.Inf(1)
		if a != "" {
			s, err := p.scalar(def.Value.Kind, a)
			if err != "" {
				return Interval{}, fail(err)
			}
			lo = s.Lo
		}
		if b != "" {
			s, err := p.scalar(def.Value.Kind, b)
			if err != "" {
				return Interval{}, fail(err)
			}
			hi = s.Hi
		}
		if lo >= hi {
			return Interval{}, fail("range is empty (the start is after the end)")
		}
		return Interval{lo, hi}, nil
	}
	for _, op := range []string{">=", "<=", ">", "<", "="} {
		v, ok := strings.CutPrefix(text, op)
		if !ok {
			continue
		}
		s, err := p.scalar(def.Value.Kind, v)
		if err != "" {
			return Interval{}, fail(err)
		}
		// Dates compare by whole period (>2024 = from 2025 on); numbers,
		// sizes and durations compare exactly (>4gb = more than 4e9 bytes).
		lo, hi := s.Lo, s.Hi
		if def.Value.Kind != VDate {
			hi = math.Nextafter(s.Lo, math.Inf(1))
		}
		switch op {
		case ">":
			return Interval{hi, math.Inf(1)}, nil
		case ">=":
			return Interval{lo, math.Inf(1)}, nil
		case "<":
			return Interval{math.Inf(-1), lo}, nil
		case "<=":
			return Interval{math.Inf(-1), hi}, nil
		}
		return s, nil
	}
	s, err := p.scalar(def.Value.Kind, text)
	if err != "" {
		return Interval{}, fail(err)
	}
	return s, nil
}

// scalar is a single value as the interval its precision implies: 2024 =
// all of 2024, 4gb = [4 GB, 5 GB), 12.5 = [12.5, 12.6). The error is a
// message, empty on success.
func (p *parser) scalar(kind ValueKind, text string) (Interval, string) {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return Interval{}, "missing value"
	}
	unitScale := func(units map[string]float64, what, unitWhat string) (Interval, string) {
		num, unit := splitUnit(text)
		mult, ok := units[unit]
		if !ok {
			return Interval{}, fmt.Sprintf("unknown %s unit `%s`", unitWhat, unit)
		}
		iv, ok := numberInterval(num, mult)
		if !ok {
			return Interval{}, fmt.Sprintf("`%s` is not a %s", text, what)
		}
		return iv, ""
	}
	switch kind {
	case VDate:
		return p.date(text)
	case VNumber:
		iv, ok := numberInterval(text, 1)
		if !ok {
			return Interval{}, fmt.Sprintf("`%s` is not a number", text)
		}
		return iv, ""
	case VSize:
		return unitScale(map[string]float64{"": 1, "b": 1, "k": 1e3, "kb": 1e3, "m": 1e6, "mb": 1e6, "g": 1e9, "gb": 1e9, "t": 1e12, "tb": 1e12}, "size", "size")
	case VDuration:
		return unitScale(map[string]float64{"": 1, "s": 1, "sec": 1, "m": 60, "min": 60, "h": 3600, "hr": 3600}, "duration", "duration")
	case VBitrate:
		return unitScale(map[string]float64{"": 1, "bps": 1, "k": 1e3, "kbps": 1e3, "m": 1e6, "mbps": 1e6, "g": 1e9, "gbps": 1e9}, "bit rate", "bit-rate")
	}
	return Interval{}, "not a numeric filter"
}

func (p *parser) date(text string) (Interval, string) {
	const day = 86_400
	today := floorDiv(p.now, day) * day
	iv := func(lo, hi int64) Interval { return Interval{float64(lo), float64(hi)} }
	switch text {
	case "today":
		return iv(today, today+day), ""
	case "yesterday":
		return iv(today-day, today), ""
	}
	notDate := fmt.Sprintf("`%s` is not a date", text)
	// Relative: 7d, 2w, 3mo, 1y — from N units ago until now.
	num, unit := splitUnit(text)
	if unit != "" && isAlpha(unit) && num != "" && !strings.Contains(num, ".") {
		n, err := strconv.ParseInt(num, 10, 64)
		if err != nil {
			return Interval{}, notDate
		}
		secs := map[string]int64{"d": day, "w": 7 * day, "mo": 30 * day, "y": 365 * day}[unit]
		if secs == 0 {
			return Interval{}, fmt.Sprintf("unknown date unit `%s` (use d, w, mo, y)", unit)
		}
		return Interval{float64(p.now - n*secs), math.Inf(1)}, ""
	}
	parts := strings.Split(text, "-")
	digits := func(s string, n int) (int64, bool) {
		if len(s) != n || !isDigits(s) {
			return 0, false
		}
		v, _ := strconv.ParseInt(s, 10, 64)
		return v, true
	}
	switch len(parts) {
	case 1:
		y, ok := digits(parts[0], 4)
		if !ok {
			return Interval{}, notDate
		}
		return iv(daysFromCivil(y, 1, 1)*day, daysFromCivil(y+1, 1, 1)*day), ""
	case 2:
		y, ok1 := digits(parts[0], 4)
		m, ok2 := digits(parts[1], 2)
		if !ok1 || !ok2 {
			return Interval{}, notDate
		}
		if m < 1 || m > 12 {
			return Interval{}, fmt.Sprintf("month %d is not 01–12", m)
		}
		ny, nm := y, m+1
		if m == 12 {
			ny, nm = y+1, 1
		}
		return iv(daysFromCivil(y, m, 1)*day, daysFromCivil(ny, nm, 1)*day), ""
	case 3:
		y, ok1 := digits(parts[0], 4)
		m, ok2 := digits(parts[1], 2)
		d, ok3 := digits(parts[2], 2)
		if !ok1 || !ok2 || !ok3 {
			return Interval{}, notDate
		}
		if m < 1 || m > 12 || d < 1 || d > daysInMonth(y, m) {
			return Interval{}, fmt.Sprintf("`%s` is not a real date", text)
		}
		start := daysFromCivil(y, m, d) * day
		return iv(start, start+day), ""
	}
	return Interval{}, notDate
}

func pattern(text string) Pattern {
	t := fold.Fold(text)
	return Pattern{Text: t, Wildcards: strings.ContainsAny(t, "*?")}
}

func unescape(r []rune) string {
	var b strings.Builder
	for i := 0; i < len(r); i++ {
		if r[i] == '\\' {
			if i+1 < len(r) {
				b.WriteRune(r[i+1])
			}
			i++
			continue
		}
		b.WriteRune(r[i])
	}
	return b.String()
}

// splitList splits on commas outside quotes.
func splitList(r []rune) [][]rune {
	var out [][]rune
	start, inQuote := 0, false
	for i := 0; i < len(r); i++ {
		switch {
		case r[i] == '\\' && inQuote:
			i++
		case r[i] == '"':
			inQuote = !inQuote
		case r[i] == ',' && !inQuote:
			out = append(out, r[start:i])
			start = i + 1
		}
	}
	return append(out, r[start:])
}

func splitUnit(text string) (string, string) {
	i := strings.IndexFunc(text, func(c rune) bool { return !(c >= '0' && c <= '9' || c == '.') })
	if i < 0 {
		return text, ""
	}
	return text[:i], text[i:]
}

// numberInterval is num × mult as an interval one unit of its last decimal
// place wide.
func numberInterval(num string, mult float64) (Interval, bool) {
	if num == "" || num[0] == '.' || num[len(num)-1] == '.' || strings.Count(num, ".") > 1 || !isNumeric(num) {
		return Interval{}, false
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return Interval{}, false
	}
	decimals := 0
	if _, d, ok := strings.Cut(num, "."); ok {
		decimals = len(d)
	}
	step := math.Pow(10, float64(-decimals))
	return Interval{v * mult, (v + step) * mult}, true
}

func isNumeric(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c == '.') {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

func isAlpha(s string) bool {
	for _, c := range s {
		if !(c >= 'a' && c <= 'z') {
			return false
		}
	}
	return true
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// daysFromCivil is days since 1970-01-01 for a proleptic Gregorian date
// (Howard Hinnant).
func daysFromCivil(y, m, d int64) int64 {
	if m <= 2 {
		y--
	}
	era := floorDiv(y, 400)
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146_097 + doe - 719_468
}

func daysInMonth(y, m int64) int64 {
	switch m {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	}
	if (y%4 == 0 && y%100 != 0) || y%400 == 0 {
		return 29
	}
	return 28
}
