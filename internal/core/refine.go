package core

import (
	"strconv"
	"strings"

	"github.com/RoaringBitmap/roaring/v2"

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
	Count uint64 `json:"count"`
}

// RefineGroup is a set of choices about one attribute.
type RefineGroup struct {
	// ID names the attribute (the app titles it): resolution, sound, shape,
	// length, codec, camera, place, picture, artist, album, genre, type,
	// modified, size.
	ID      string         `json:"id"`
	Options []RefineOption `json:"options"`
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
func (ld *Laserdot) Refinements(q string) ([]RefineGroup, error) {
	if strings.TrimSpace(q) == "" || strings.HasPrefix(strings.TrimSpace(q), ">") {
		return []RefineGroup{}, nil
	}
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	type source struct {
		s    *index.Store
		hits *roaring.Bitmap
	}
	var sources []source
	var total uint64
	stores := []*index.Store{ld.store}
	contents := []*index.ContentIndex{ld.content}
	for _, d := range ld.drives {
		stores = append(stores, d.store)
		contents = append(contents, nil)
	}
	for i, s := range stores {
		hits, err := queryHits(s, contents[i], q)
		if err != nil {
			return nil, err
		}
		if !hits.IsEmpty() {
			sources = append(sources, source{s, hits})
			total += hits.GetCardinality()
		}
	}
	out := []RefineGroup{}
	if total < 2 {
		return out, nil // nothing to choose between
	}
	have := strings.ToLower(q)
	count := func(token string) uint64 {
		parsed, err := query.Parse(token)
		if err != nil {
			return 0
		}
		c, err := query.Compile(parsed, false)
		if err != nil {
			return 0
		}
		var n uint64
		for _, src := range sources {
			if bm, err := src.s.Execute(c); err == nil {
				n += bm.AndCardinality(src.hits)
			}
		}
		return n
	}
	groups := map[string]*RefineGroup{}
	var order []string
	add := func(group string, o RefineOption) {
		if o.Count == 0 || o.Count >= total || strings.Contains(have, strings.ToLower(o.Token)) {
			return
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
			add(c.group, RefineOption{Token: c.token, Label: c.label, Key: c.key, Count: count(c.token)})
		}
	}
	facets := func(group, key string, id query.FilterID, scope query.Scope, titled bool) {
		merged := map[string]uint64{}
		var values []string
		for _, src := range sources {
			for _, f := range src.s.FacetCountsIn(id, scope, src.hits, RefineFacetLimit) {
				if _, ok := merged[f.Value]; !ok {
					values = append(values, f.Value)
				}
				merged[f.Value] += f.Count
			}
		}
		for _, v := range values {
			label := v
			if titled {
				label = displayValue(v)
			}
			add(group, RefineOption{Token: key + ":" + quoteValue(v), Label: label, Count: merged[v]})
		}
	}
	videos, images, audio, docs := count("kind:video"), count("kind:image"), count("kind:audio"), count("kind:document")
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
			add("type", RefineOption{Token: "ext:" + e, Label: strings.ToUpper(e), Count: count("ext:" + e)})
		}
	}
	try(refineCommon)
	for _, id := range order {
		if g := groups[id]; len(g.Options) > 0 {
			out = append(out, *g)
		}
	}
	return out, nil
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
