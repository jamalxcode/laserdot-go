package core

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/jamalxcode/laserdot-go/internal/fold"
	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/query"
)

// Refinements (the owner: "show related attributes that can be selected in
// addition to the search term"): for the results of a search, the attributes
// they actually have — resolutions, sound or not, portrait or landscape,
// length and codec for videos; shape, camera, location and kind of picture
// for images; artist, album and genre for music; the type for documents;
// and date and size for everything — each with how many results have it.
// Choosing one adds its token to the query (it is ordinary query language,
// so the hint line explains it and it can be typed by hand too). Only what
// narrows the results is offered: a value no result has, or every result
// has, is left out, and so is one the query already asks for.

// RefineOption is one choice: Token goes into the query.
type RefineOption struct {
	Token string `json:"token"`
	// Label is shown as it is (a value: "1080p", "hevc", "Canon EOS R5");
	// when Key is set the app shows its own words for it ("With sound").
	Label string `json:"label"`
	Key   string `json:"key,omitempty"`
	// Count: results it gives with the other groups' choices applied (0:
	// none any more; the app greys it out).
	Count uint64 `json:"count"`
	// Selected: q already has it (Token is then as written in q).
	Selected bool `json:"selected,omitempty"`
	// Excluded: q leaves it out ("-ext:pdf", the owner's "exclude
	// choices"); Token is the "-" token as written, Selected is set too
	// (clicking takes it out of q) and Count is 0.
	Excluded bool `json:"excluded,omitempty"`
}

// RefineGroup is a set of choices about one attribute.
type RefineGroup struct {
	// ID names the attribute (the app titles it): resolution, sound, shape,
	// length, codec, camera, place, picture, artist, album, genre, type,
	// modified, size, and where: drive (with a drive attached) and folder.
	ID string `json:"id"`
	// Family: related groups shown together and shaded alike (where, what,
	// file, picture, time, photo, music).
	Family  string         `json:"family,omitempty"`
	Options []RefineOption `json:"options"`
}

// refineOrder (the owner: "start top down … drive, folder, kind, size,
// resolution, sound … starting large then going granular, with related
// properties"): every group's place and family. Groups come in this order,
// whatever kinds of results produced them.
var refineOrder = []struct{ id, family string }{
	{"drive", "where"}, {"folder", "where"},
	{"kind", "what"}, {"type", "what"},
	{"size", "file"}, {"modified", "file"},
	{"resolution", "picture"}, {"shape", "picture"}, {"hdr", "picture"}, {"codec", "picture"},
	{"length", "time"}, {"sound", "time"},
	{"camera", "photo"}, {"place", "photo"}, {"picture", "photo"},
	{"artist", "music"}, {"album", "music"}, {"genre", "music"},
}

// refineRank: a group's place in refineOrder (unknown ones last) and family.
func refineRank(id string) (int, string) {
	for i, o := range refineOrder {
		if o.id == id {
			return i, o.family
		}
	}
	return len(refineOrder), "other"
}

// RefineFacetLimit: values offered for a text attribute (codec, camera…).
const RefineFacetLimit = 6

type refineCandidate struct {
	group, token, label, key string
}

var (
	refineVideo = []refineCandidate{
		{"resolution", "res:8k", "8K", ""}, {"resolution", "res:4k", "4K", ""}, {"resolution", "res:1440p", "1440p", ""},
		{"resolution", "res:1080p", "1080p", ""}, {"resolution", "res:720p", "720p", ""}, {"resolution", "res:sd", "SD", ""},
		{"sound", "a.count:>=1", "", "sound.with"}, {"sound", "a.count:0", "", "sound.none"},
		{"hdr", "hdr:true", "", "hdr.yes"},
	}
	refineShape = []refineCandidate{
		{"shape", "orientation:portrait", "", "shape.portrait"}, {"shape", "orientation:landscape", "", "shape.landscape"},
		{"shape", "orientation:square", "", "shape.square"},
	}
	refineLength = []refineCandidate{
		{"length", "duration:<1min", "", "length.short"}, {"length", "duration:1min..10min", "", "length.medium"},
		{"length", "duration:10min..1h", "", "length.long"}, {"length", "duration:>1h", "", "length.verylong"},
	}
	refineImage = []refineCandidate{
		{"place", "gps:true", "", "place.yes"},
		{"picture", "screenshot:true", "", "picture.screenshot"}, {"picture", "live:true", "", "picture.live"},
		{"picture", "raw:true", "", "picture.raw"},
	}
	refineDocTypes = []string{"pdf", "docx", "doc", "pages", "txt", "md", "rtf", "xlsx", "xls", "numbers", "csv", "pptx", "key", "epub"}
	refineCommon   = []refineCandidate{
		{"modified", "modified:today", "", "modified.today"}, {"modified", "since:7d", "", "modified.week"},
		{"modified", "since:30d", "", "modified.month"}, {"modified", "since:365d", "", "modified.year"},
		{"size", "fsize:<1mb", "", "size.small"}, {"size", "fsize:1mb..100mb", "", "size.medium"},
		{"size", "fsize:100mb..1gb", "", "size.large"}, {"size", "fsize:>1gb", "", "size.huge"},
	}
)

// Refinements answers for q across this Mac's index and every attached drive.
//
// Choosing keeps every other choice available (the owner: "keep the other
// options still active so the user can add more"), the way faceted search
// works: the tokens of q that are choices (res:1080p, a.count:0, ext:pdf…)
// are set apart from the rest (the base). The groups and their values come
// from the base's results, so they stay put as choices are made; each
// group's counts are taken with every other group's choices applied but not
// its own, so another value of the same group can be switched to, and a
// value no longer matching shows 0 (the app greys it out). Choices already
// in q come back marked Selected, with their token as written in q.
func (ld *Laserdot) Refinements(q string) ([]RefineGroup, error) {
	out := []RefineGroup{}
	if strings.TrimSpace(q) == "" || strings.HasPrefix(strings.TrimSpace(q), ">") {
		return out, nil
	}
	// The choices in q, by group, and the rest.
	selected := map[string][]string{} // group → tokens as written
	excluded := map[string]string{}   // the token left out (lower case, no "-") → as written
	var baseToks []string
	for _, tok := range splitTokens(q) {
		if rest, ok := strings.CutPrefix(tok, "-"); ok && refineGroupOf(rest) != "" {
			excluded[strings.ToLower(rest)] = tok // stays in the base: it narrows every count
		}
		if g := refineGroupOf(tok); g != "" {
			selected[g] = append(selected[g], tok)
		} else {
			baseToks = append(baseToks, tok)
		}
	}
	base := strings.Join(baseToks, " ")
	if strings.TrimSpace(base) == "" && len(selected["kind"]) > 0 && len(selected) > 1 {
		// Only choices: the kind ("kind:video res:4k") is what the others narrow.
		base = strings.Join(selected["kind"], " ")
		delete(selected, "kind")
	}
	if strings.TrimSpace(base) == "" {
		base, selected = q, map[string][]string{} // only choices: nothing set apart
	}
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	type source struct {
		s     *index.Store
		drive string          // the drive's name ("": this Mac)
		hits  *roaring.Bitmap // the base's results
		cache map[string]*roaring.Bitmap
	}
	var sources []*source
	var total uint64
	stores := []*index.Store{ld.store}
	contents := []*index.ContentIndex{ld.content}
	names := []string{""}
	for _, d := range ld.drives {
		stores = append(stores, d.store)
		contents = append(contents, nil)
		names = append(names, d.name)
	}
	for i, s := range stores {
		hits, err := queryHits(s, contents[i], base)
		if err != nil {
			return nil, err
		}
		if !hits.IsEmpty() {
			sources = append(sources, &source{s, names[i], hits, map[string]*roaring.Bitmap{}})
			total += hits.GetCardinality()
		}
	}
	if total < 2 && len(excluded) == 0 {
		return out, nil // nothing to choose between
	}
	// tokenSet: the entries a token selects (one evaluation per token).
	tokenSet := func(src *source, token string) *roaring.Bitmap {
		key := strings.ToLower(token)
		if bm, ok := src.cache[key]; ok {
			return bm
		}
		bm := roaring.New()
		if parsed, err := query.Parse(token); err == nil {
			if c, err := query.Compile(parsed, false); err == nil {
				if r, err := src.s.Execute(c); err == nil {
					bm = r
				}
			}
		}
		src.cache[key] = bm
		return bm
	}
	// Counts in the base's results (which values exist to choose from).
	countBase := func(token string) uint64 {
		var n uint64
		for _, src := range sources {
			n += tokenSet(src, token).AndCardinality(src.hits)
		}
		return n
	}
	// within: the base's results with every other group's choices applied.
	within := map[string][]*roaring.Bitmap{}
	hitsFor := func(group string) []*roaring.Bitmap {
		if w, ok := within[group]; ok {
			return w
		}
		w := make([]*roaring.Bitmap, len(sources))
		for i, src := range sources {
			b := src.hits.Clone()
			for g, toks := range selected {
				if g == group {
					continue
				}
				for _, t := range toks {
					b.And(tokenSet(src, t))
				}
			}
			w[i] = b
		}
		within[group] = w
		return w
	}
	isSelected := func(group, token string) (string, bool) {
		for _, t := range selected[group] {
			if strings.EqualFold(t, token) {
				return t, true
			}
		}
		return "", false
	}
	groups := map[string]*RefineGroup{}
	var order []string
	add := func(group string, o RefineOption) {
		if written, ok := excluded[strings.ToLower(o.Token)]; ok {
			delete(excluded, strings.ToLower(o.Token)) // shown once
			o.Token, o.Selected, o.Excluded, o.Count = written, true, true, 0
			g := groups[group]
			if g == nil {
				g = &RefineGroup{ID: group}
				groups[group] = g
				order = append(order, group)
			}
			g.Options = append(g.Options, o)
			return
		}
		written, sel := isSelected(group, o.Token)
		if !sel {
			if b := countBase(o.Token); b == 0 || b >= total {
				return // nothing to choose: no result, or every result, has it
			}
		}
		if g := groups[group]; g != nil {
			for _, x := range g.Options {
				if strings.EqualFold(x.Token, o.Token) {
					return
				}
			}
		}
		if sel {
			o.Token, o.Selected = written, true
		}
		w := hitsFor(group)
		o.Count = 0
		for i, src := range sources {
			o.Count += tokenSet(src, o.Token).AndCardinality(w[i])
		}
		g := groups[group]
		if g == nil {
			g = &RefineGroup{ID: group}
			groups[group] = g
			order = append(order, group)
		}
		g.Options = append(g.Options, o)
	}
	try := func(cands []refineCandidate) {
		for _, c := range cands {
			add(c.group, RefineOption{Token: c.token, Label: c.label, Key: c.key})
		}
	}
	facets := func(group, key string, id query.FilterID, scope query.Scope, titled bool) {
		seen := map[string]bool{}
		var values []string
		for _, src := range sources {
			for _, f := range src.s.FacetCountsIn(id, scope, src.hits, RefineFacetLimit) {
				if !seen[f.Value] {
					seen[f.Value] = true
					values = append(values, f.Value)
				}
			}
		}
		for _, v := range values {
			label := v
			if titled {
				label = displayValue(v)
			}
			add(group, RefineOption{Token: key + ":" + quoteValue(v), Label: label})
		}
	}
	// Where (the owner: "choose the drives and possibly directories it
	// searches, have it as a choice"): this Mac or a drive, when a drive is
	// attached; and the folders the results are in.
	// What the results are (videos, documents, folders…): the first choice
	// (it was a row of its own; one row of choices is calmer).
	for _, k := range query.Kinds {
		add("kind", RefineOption{Token: "kind:" + k, Key: "kind." + k})
	}
	if len(ld.drives) > 0 {
		add("drive", RefineOption{Token: "volume:local", Key: "drive.local"})
		for _, d := range ld.drives {
			add("drive", RefineOption{Token: "volume:" + quoteValue(fold.Fold(d.name)), Label: d.name})
		}
	}
	for _, src := range sources {
		for _, f := range resultFolders(src.s, src.hits, RefineFolderLimit) {
			label := src.s.Name(f)
			if src.drive != "" {
				label = src.drive + " › " + label
			}
			add("folder", RefineOption{Token: "in:" + quoteValue(folderToken(src.s.Path(f))), Label: label})
		}
	}
	videos, images, audio, docs := countBase("kind:video"), countBase("kind:image"), countBase("kind:audio"), countBase("kind:document")
	if videos > 0 {
		try(refineVideo[:6]) // resolution
		try(refineVideo[6:8])
		try(refineShape)
		try(refineLength)
		facets("codec", "codec", query.FCodec, query.Video, false)
		try(refineVideo[8:])
	}
	if images > 0 {
		if videos == 0 {
			try(refineVideo[:6])
			try(refineShape)
		}
		facets("camera", "camera", query.FCamera, query.NoScope, true)
		try(refineImage)
	}
	if audio > 0 {
		if videos == 0 {
			try(refineLength)
		}
		facets("artist", "artist", query.FArtist, query.NoScope, true)
		facets("album", "album", query.FAlbum, query.NoScope, true)
		facets("genre", "genre", query.FGenre, query.NoScope, true)
	}
	if docs > 0 {
		for _, e := range refineDocTypes {
			add("type", RefineOption{Token: "ext:" + e, Label: strings.ToUpper(e)})
		}
	}
	try(refineCommon)
	// A choice typed by hand that is not among the values above (another
	// camera, ext:heic…) still shows, selected, so it can be removed.
	for g, toks := range selected {
		for _, t := range toks {
			label := t
			if i := strings.Index(t, ":"); i >= 0 {
				label = strings.Trim(t[i+1:], `"`)
			}
			if g == "folder" {
				label = filepath.Base(label) // a folder by its name, not its whole path
			}
			add(g, RefineOption{Token: t, Label: label})
		}
	}
	// A value left out by hand that is not among the values above.
	for rest, written := range excluded {
		label := written
		if i := strings.Index(written, ":"); i >= 0 {
			label = strings.Trim(written[i+1:], `"`)
		}
		add(refineGroupOf(rest), RefineOption{Token: rest, Label: label})
	}
	slices.SortStableFunc(order, func(a, b string) int {
		ra, _ := refineRank(a)
		rb, _ := refineRank(b)
		return cmp.Compare(ra, rb)
	})
	for _, id := range order {
		if g := groups[id]; len(g.Options) > 0 {
			_, g.Family = refineRank(id)
			out = append(out, *g)
		}
	}
	return out, nil
}

// RefineFolderLimit: folders offered for one index.
const RefineFolderLimit = 8

// resultFolders: the folders to choose between for hits, most results
// first. Under each indexed folder, it goes down while one folder holds all
// of its results (home → Movies → Trips), then offers that folder's
// subfolders that hold results.
func resultFolders(s *index.Store, hits *roaring.Bitmap, limit int) []uint32 {
	type counted struct {
		id uint32
		n  uint64
	}
	var all []counted
	for _, root := range s.RootIDs() {
		dir := root
		inDir := s.Within(dir).AndCardinality(hits)
		if inDir == 0 {
			continue
		}
		var kids []counted
		for depth := 0; depth < 16; depth++ {
			kids = kids[:0]
			for _, c := range s.Children(dir) {
				if s.IsDir(c) && !s.IsSymlink(c) {
					if n := s.Within(c).AndCardinality(hits); n > 0 {
						kids = append(kids, counted{c, n})
					}
				}
			}
			if len(kids) == 1 {
				n := kids[0].n
				if hits.Contains(kids[0].id) {
					n++ // the folder itself matches (Trips for "trip")
				}
				if n == inDir {
					dir, inDir = kids[0].id, kids[0].n // one folder holds them all: look inside it
					continue
				}
			}
			break
		}
		all = append(all, kids...)
	}
	slices.SortStableFunc(all, func(a, b counted) int { return cmp.Compare(b.n, a.n) })
	out := make([]uint32, 0, min(limit, len(all)))
	for _, c := range all {
		if len(out) == limit {
			break
		}
		out = append(out, c.id)
	}
	return out
}

// folderToken: a folder's path as the in: filter takes it (~ for home).
func folderToken(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rest, ok := strings.CutPrefix(p, home); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
			return "~" + rest
		}
	}
	return p
}

// refineGroups: which group a choice's filter belongs to.
var refineGroups = map[string]string{
	"res": "resolution", "a.count": "sound", "orientation": "shape", "duration": "length", "codec": "codec",
	"hdr": "hdr", "camera": "camera", "gps": "place", "screenshot": "picture", "live": "picture", "raw": "picture",
	"artist": "artist", "album": "album", "genre": "genre", "ext": "type", "modified": "modified", "since": "modified",
	"fsize": "size", "volume": "drive", "in": "folder", "kind": "kind",
}

// refineGroupOf: the group of a token that is a choice ("" when it is not:
// a word, a negation, another filter).
func refineGroupOf(tok string) string {
	i := strings.Index(tok, ":")
	if i <= 0 || strings.HasPrefix(tok, "-") {
		return ""
	}
	return refineGroups[strings.ToLower(tok[:i])]
}

// splitTokens splits a query at spaces outside quotes and parentheses.
func splitTokens(q string) []string {
	var out []string
	var cur strings.Builder
	inQuote, depth := false, 0
	for _, r := range q {
		switch {
		case r == '"':
			inQuote = !inQuote
		case !inQuote && r == '(':
			depth++
		case !inQuote && r == ')' && depth > 0:
			depth--
		case !inQuote && depth == 0 && (r == ' ' || r == '\t'):
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// queryHits: the entries of s matching q by name and filters (what a
// search's results are, without typo fallback or content-only matches).
func queryHits(s *index.Store, content *index.ContentIndex, q string) (*roaring.Bitmap, error) {
	parsed, err := query.Parse(q)
	if err != nil {
		return nil, err
	}
	parts, err := splitContent(parsed)
	if err != nil {
		return nil, err
	}
	if parts.query.IsEmpty() && len(parts.filters) == 0 {
		return roaring.New(), nil
	}
	c, err := query.Compile(parts.query, false)
	if err != nil {
		return nil, err
	}
	hits, err := s.Execute(c)
	if err != nil {
		return nil, err
	}
	for _, f := range parts.filters {
		set := roaring.New()
		if content != nil {
			paths, err := content.Matching(f.expr)
			if err != nil {
				return nil, err
			}
			set.AddMany(contentIDs(s, paths))
		}
		if f.negated {
			hits.AndNot(set)
		} else {
			hits.And(set)
		}
	}
	return hits, nil
}

// quoteValue quotes a value with spaces or punctuation for the query.
func quoteValue(v string) string {
	if strings.ContainsAny(v, " \t\"'():,") {
		return strconv.Quote(v)
	}
	return v
}

// displayValue: a folded name made readable ("canon eos r5" → "Canon Eos
// R5").
func displayValue(v string) string {
	words := strings.Fields(v)
	for i, w := range words {
		r := []rune(w)
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}
