package index

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/jamalxcode/laserdot-go/internal/fold"
	"github.com/jamalxcode/laserdot-go/internal/media"
	"github.com/jamalxcode/laserdot-go/internal/query"
)

// Media columns and bitmaps (spec §2.1.1c–d).
//
//   - Numeric columns exist only for media rows (row[entry] maps an entry to
//     its row), so non-media files cost 4 bytes each.
//   - Text attributes (codec, language, camera, artist…) are facets: folded
//     value → bitmap of entry ids. A filter unions the bitmaps of the keys
//     it matches, so lang:ar is a handful of bitmap ORs.
//   - Track lists are kept for track groups (a(lang:ar channels:5.1)).

type mediaFlag uint8

const (
	flagHasVideo mediaFlag = iota
	flagHasAudio
	flagHasSubs
	flagVFR
	flagAtmos
	flagLossless
	flagForced
	flagGPS
	flagLive
	flagScreenshot
	flagSidecar
	numFlags
)

type facetKey struct {
	id    query.FilterID
	scope query.Scope
}

// MediaIndex holds the media columns of a Store.
type MediaIndex struct {
	row        []uint32 // entry id → media row, or None
	all        *roaring.Bitmap
	durationMS []uint32
	bitrate    []uint32
	fpsMilli   []uint32
	bitDepth   []uint8
	bpp        []float32
	sampleRate []uint32
	counts     [][3]uint8 // video, audio, subtitle tracks
	iso        []uint32
	aperture   []float32
	focal      []float32
	taken      []int64
	year       []uint32
	tracks     [][]media.Track
	facets     map[facetKey]map[string]*roaring.Bitmap
	flags      [numFlags]*roaring.Bitmap
}

func newMediaIndex() *MediaIndex {
	m := &MediaIndex{all: roaring.New(), facets: map[facetKey]map[string]*roaring.Bitmap{}}
	for i := range m.flags {
		m.flags[i] = roaring.New()
	}
	return m
}

// grow makes room for entry id.
func (m *MediaIndex) grow(id uint32) {
	for uint32(len(m.row)) <= id {
		m.row = append(m.row, None)
	}
}

func (m *MediaIndex) has(id uint32) bool { return int(id) < len(m.row) && m.row[id] != None }

func (m *MediaIndex) setFlag(f mediaFlag, id uint32)   { m.flags[f].Add(id) }
func (m *MediaIndex) clearFlag(f mediaFlag, id uint32) { m.flags[f].Remove(id) }

// Common aspect ratios; a frame gets every name within 3 % of its ratio.
var aspects = []struct {
	name  string
	ratio float64
}{
	{"1:1", 1}, {"5:4", 1.25}, {"4:3", 4.0 / 3}, {"3:2", 1.5}, {"16:10", 1.6}, {"16:9", 16.0 / 9},
	{"1.85:1", 1.85}, {"2:1", 2}, {"21:9", 7.0 / 3}, {"2.39:1", 2.39}, {"9:16", 9.0 / 16}, {"3:4", 0.75},
	{"2:3", 2.0 / 3},
}

// ChannelName is the channels: value for a channel count.
func ChannelName(ch uint8) string {
	switch ch {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	}
	return ""
}

func hdrNames(hdr uint8) []string {
	var out []string
	for _, h := range []struct {
		bit  uint8
		name string
	}{{media.HDRHDR10, "hdr10"}, {media.HDRHLG, "hlg"}, {media.HDRDV, "dv"}} {
		if hdr&h.bit != 0 {
			out = append(out, h.name)
		}
	}
	return out
}

func (m *MediaIndex) facet(id query.FilterID, scope query.Scope, value string, entry uint32) {
	key := fold.Fold(strings.TrimSpace(value))
	if key == "" {
		return
	}
	fk := facetKey{id, scope}
	mm := m.facets[fk]
	if mm == nil {
		mm = map[string]*roaring.Bitmap{}
		m.facets[fk] = mm
	}
	bm := mm[key]
	if bm == nil {
		bm = roaring.New()
		mm[key] = bm
	}
	bm.Add(entry)
}

// langKeys are every key a stored language matches: ar → ar, ara.
func langKeys(code string) []string {
	if v := media.LangAliases(code); len(v) > 0 {
		return v
	}
	return []string{code}
}

// add stores rec for entry (entries are added in ascending id order).
func (m *MediaIndex) add(entry uint32, rec *media.Record) {
	m.grow(entry)
	m.row[entry] = uint32(len(m.durationMS))
	m.all.Add(entry)
	video := rec.Video()
	var fps, vbitrate uint32
	if video != nil {
		fps = video.FPSMilli
		vbitrate = video.Bitrate
	}
	if vbitrate == 0 {
		vbitrate = rec.Bitrate
	}
	pixels := float64(rec.Width) * float64(rec.Height) * float64(fps) / 1000
	m.durationMS = append(m.durationMS, uint32(min(rec.DurationMS, math.MaxUint32)))
	m.bitrate = append(m.bitrate, rec.Bitrate)
	m.fpsMilli = append(m.fpsMilli, fps)
	var depth uint8
	if video != nil {
		depth = video.BitDepth
	}
	m.bitDepth = append(m.bitDepth, depth)
	var bpp float32
	if vbitrate > 0 && pixels > 0 {
		bpp = float32(float64(vbitrate) / pixels)
	}
	m.bpp = append(m.bpp, bpp)
	var counts [3]uint8
	var maxRate uint32
	for i := range rec.Tracks {
		t := &rec.Tracks[i]
		var scope query.Scope
		switch t.Kind {
		case media.TrackVideo:
			scope = query.Video
		case media.TrackAudio:
			scope = query.Audio
		case media.TrackSubtitle:
			scope = query.Subtitle
		default:
			continue
		}
		if counts[scope-1] < 255 {
			counts[scope-1]++
		}
		switch scope {
		case query.Video:
			m.setFlag(flagHasVideo, entry)
			m.facet(query.FCodec, scope, t.Codec, entry)
			m.facet(query.FProfile, scope, t.Profile, entry)
			m.facet(query.FPixFmt, scope, t.PixFmt, entry)
			for _, h := range hdrNames(t.HDR) {
				m.facet(query.FHdr, scope, h, entry)
			}
		case query.Audio:
			m.setFlag(flagHasAudio, entry)
			m.facet(query.FAcodec, scope, t.Codec, entry)
			m.facet(query.FChannels, scope, ChannelName(t.Channels), entry)
			if t.Atmos {
				m.setFlag(flagAtmos, entry)
			}
			if t.Lossless {
				m.setFlag(flagLossless, entry)
			}
			maxRate = max(maxRate, t.SampleRate)
		case query.Subtitle:
			m.setFlag(flagHasSubs, entry)
			m.facet(query.FSubFormat, scope, t.Codec, entry)
			if t.Forced {
				m.setFlag(flagForced, entry)
			}
		}
		if t.Lang != "" {
			for _, k := range langKeys(t.Lang) {
				m.facet(query.FLang, scope, k, entry)
			}
		}
	}
	m.counts = append(m.counts, counts)
	m.sampleRate = append(m.sampleRate, maxRate)
	if rec.VFR {
		m.setFlag(flagVFR, entry)
	}
	m.facet(query.FContainer, query.NoScope, rec.Container, entry)
	if video != nil && rec.Width > 0 && rec.Height > 0 {
		r := float64(rec.Width) / float64(rec.Height)
		for _, a := range aspects {
			if math.Abs(r/a.ratio-1) <= 0.03 {
				m.facet(query.FAspectRatio, query.Video, a.name, entry)
			}
		}
	}
	m.facet(query.FArtist, query.NoScope, rec.Tags.Artist, entry)
	m.facet(query.FAlbum, query.NoScope, rec.Tags.Album, entry)
	m.facet(query.FGenre, query.NoScope, rec.Tags.Genre, entry)
	m.facet(query.FTitle, query.NoScope, rec.Tags.Title, entry)
	m.year = append(m.year, rec.Tags.Year)
	x := media.Exif{}
	if rec.Exif != nil {
		x = *rec.Exif
	}
	m.facet(query.FCamera, query.NoScope, x.Camera, entry)
	m.facet(query.FLens, query.NoScope, x.Lens, entry)
	if x.GPS {
		m.setFlag(flagGPS, entry)
	}
	if x.Screenshot {
		m.setFlag(flagScreenshot, entry)
	}
	m.iso = append(m.iso, x.ISO)
	m.aperture = append(m.aperture, x.Aperture)
	m.focal = append(m.focal, x.Focal)
	taken := int64(math.MinInt64)
	if x.HasTaken {
		taken = x.Taken
	}
	m.taken = append(m.taken, taken)
	m.tracks = append(m.tracks, slices.Clone(rec.Tracks))
}

// rows keeps the media entries of pool whose value is known and inside iv.
func (m *MediaIndex) rows(s *Store, pool *roaring.Bitmap, iv query.Interval, value func(r uint32) (float64, bool)) *roaring.Bitmap {
	return s.scan(roaring.And(pool, m.all), 4096, func(id uint32) bool {
		v, ok := value(m.row[id])
		return ok && iv.Contains(v)
	})
}

func nonzero(v float64) (float64, bool) { return v, v > 0 }

func (m *MediaIndex) facetUnion(id query.FilterID, scope query.Scope, v *query.CValue) *roaring.Bitmap {
	out := roaring.New()
	for key, bm := range m.facets[facetKey{id, scope}] {
		hit := false
		for _, nm := range v.Text {
			if nm.Match(key) {
				hit = true
				break
			}
		}
		hit = hit || slices.Contains(v.Enum, key)
		if hit {
			out.Or(bm)
		}
	}
	return out
}

// FacetValue is a facet value with its entry count.
type FacetValue struct {
	Value string `json:"value"`
	Count uint64 `json:"count"`
}

// FacetValues are a facet's values, most common first (cheat-sheet live
// counts and completions, §8.9.5).
func (s *Store) FacetValues(id query.FilterID, scope query.Scope) []FacetValue {
	var out []FacetValue
	live := s.Live()
	for k, bm := range s.media.facets[facetKey{id, scope}] {
		if n := bm.AndCardinality(live); n > 0 {
			out = append(out, FacetValue{k, n})
		}
	}
	slices.SortFunc(out, func(a, b FacetValue) int {
		if a.Count != b.Count {
			if a.Count > b.Count {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Value, b.Value)
	})
	return out
}

// evalMedia evaluates a media filter over cand; ok is false when f is not
// a media filter.
func (s *Store) evalMedia(f *query.CFilter, cand *roaring.Bitmap) (*roaring.Bitmap, bool) {
	m := s.media
	natural := func(sc query.Scope) query.Scope {
		if f.Scope != query.NoScope {
			return f.Scope
		}
		return sc
	}
	boolean := func(set, domain *roaring.Bitmap) *roaring.Bitmap {
		if f.Value.Bool {
			return roaring.And(cand, set)
		}
		return roaring.AndNot(roaring.And(cand, domain), set)
	}
	images := s.kindBM[media.Image]
	iv := f.Value.Interval
	rows := func(value func(r uint32) (float64, bool)) *roaring.Bitmap { return m.rows(s, cand, iv, value) }
	switch f.ID {
	case query.FCodec, query.FProfile, query.FPixFmt, query.FAspectRatio:
		return roaring.And(cand, m.facetUnion(f.ID, natural(query.Video), &f.Value)), true
	case query.FAcodec, query.FChannels:
		return roaring.And(cand, m.facetUnion(f.ID, natural(query.Audio), &f.Value)), true
	case query.FLang:
		return roaring.And(cand, m.facetUnion(f.ID, natural(query.Audio), &f.Value)), true
	case query.FSubFormat:
		return roaring.And(cand, m.facetUnion(f.ID, query.Subtitle, &f.Value)), true
	case query.FContainer, query.FArtist, query.FAlbum, query.FGenre, query.FTitle, query.FCamera, query.FLens:
		return roaring.And(cand, m.facetUnion(f.ID, query.NoScope, &f.Value)), true
	case query.FHdr:
		anyHDR := m.facetUnion(query.FHdr, query.Video, &query.CValue{Enum: []string{"hdr10", "hlg", "dv"}})
		out := roaring.New()
		for _, v := range f.Value.Enum {
			switch v {
			case "true":
				out.Or(anyHDR)
			case "false":
				out.Or(roaring.AndNot(m.flags[flagHasVideo], anyHDR))
			case "hdr10+":
				// HDR10+ lives in per-frame SEI data: not visible to Tier 1.
			default:
				out.Or(m.facetUnion(query.FHdr, query.Video, &query.CValue{Enum: []string{v}}))
			}
		}
		return roaring.And(cand, out), true
	case query.FVfr:
		return boolean(m.flags[flagVFR], m.flags[flagHasVideo]), true
	case query.FAtmos:
		return boolean(m.flags[flagAtmos], m.flags[flagHasAudio]), true
	case query.FLossless:
		return boolean(m.flags[flagLossless], m.flags[flagHasAudio]), true
	case query.FForced:
		return boolean(m.flags[flagForced], m.flags[flagHasSubs]), true
	case query.FSidecar:
		return boolean(m.flags[flagSidecar], m.flags[flagHasVideo]), true
	case query.FGps:
		return boolean(m.flags[flagGPS], images), true
	case query.FScreenshot:
		return boolean(m.flags[flagScreenshot], images), true
	case query.FLive:
		return boolean(m.flags[flagLive], roaring.Or(images, m.flags[flagHasVideo])), true
	case query.FDuration:
		return rows(func(r uint32) (float64, bool) { return nonzero(float64(m.durationMS[r]) / 1000) }), true
	case query.FBitrate:
		return rows(func(r uint32) (float64, bool) { return nonzero(float64(m.bitrate[r])) }), true
	case query.FFps:
		return rows(func(r uint32) (float64, bool) { return nonzero(float64(m.fpsMilli[r]) / 1000) }), true
	case query.FBitDepth:
		return rows(func(r uint32) (float64, bool) { return nonzero(float64(m.bitDepth[r])) }), true
	case query.FBpp:
		return rows(func(r uint32) (float64, bool) { return nonzero(float64(m.bpp[r])) }), true
	case query.FSampleRate:
		return rows(func(r uint32) (float64, bool) { return nonzero(float64(m.sampleRate[r])) }), true
	case query.FIso:
		return rows(func(r uint32) (float64, bool) { return nonzero(float64(m.iso[r])) }), true
	case query.FAperture:
		return rows(func(r uint32) (float64, bool) { return nonzero(float64(m.aperture[r])) }), true
	case query.FFocal:
		return rows(func(r uint32) (float64, bool) { return nonzero(float64(m.focal[r])) }), true
	case query.FYear:
		return rows(func(r uint32) (float64, bool) { return nonzero(float64(m.year[r])) }), true
	case query.FTaken:
		return rows(func(r uint32) (float64, bool) { return float64(m.taken[r]), m.taken[r] != math.MinInt64 }), true
	case query.FTrackCount:
		i := 1
		switch f.Scope {
		case query.Video:
			i = 0
		case query.Subtitle:
			i = 2
		}
		return rows(func(r uint32) (float64, bool) { return float64(m.counts[r][i]), true }), true
	}
	return nil, false
}

// group: entries of pool with one scope track matching every filter.
func (s *Store) group(scope query.Scope, filters []query.CFilter, pool *roaring.Bitmap) *roaring.Bitmap {
	m := s.media
	kind := media.TrackKind(scope) // Video=1, Audio=2, Subtitle=3 in both
	return s.scan(roaring.And(pool, m.all), 1024, func(id uint32) bool {
		for i := range m.tracks[m.row[id]] {
			t := &m.tracks[m.row[id]][i]
			if t.Kind != kind {
				continue
			}
			all := true
			for j := range filters {
				if !trackMatches(&filters[j], t) {
					all = false
					break
				}
			}
			if all {
				return true
			}
		}
		return false
	})
}

func trackMatches(f *query.CFilter, t *media.Track) bool {
	text := func(v string) bool {
		if v == "" {
			return false
		}
		fv := fold.Fold(v)
		for _, nm := range f.Value.Text {
			if nm.Match(fv) {
				return true
			}
		}
		return slices.Contains(f.Value.Enum, v)
	}
	num := func(x float64) bool { return x > 0 && f.Value.Interval.Contains(x) }
	switch f.ID {
	case query.FLang:
		if t.Lang == "" {
			return false
		}
		return slices.ContainsFunc(langKeys(t.Lang), text)
	case query.FChannels:
		return text(ChannelName(t.Channels))
	case query.FAcodec, query.FCodec, query.FSubFormat:
		return text(t.Codec)
	case query.FProfile:
		return text(t.Profile)
	case query.FPixFmt:
		return text(t.PixFmt)
	case query.FHdr:
		for _, v := range f.Value.Enum {
			switch v {
			case "true":
				if t.HDR != 0 {
					return true
				}
			case "false":
				if t.HDR == 0 {
					return true
				}
			default:
				if slices.Contains(hdrNames(t.HDR), v) {
					return true
				}
			}
		}
		return false
	case query.FBitDepth:
		return num(float64(t.BitDepth))
	case query.FFps:
		return num(float64(t.FPSMilli) / 1000)
	case query.FSampleRate:
		return num(float64(t.SampleRate))
	case query.FAtmos:
		return t.Atmos == f.Value.Bool
	case query.FLossless:
		return t.Lossless == f.Value.Bool
	case query.FForced:
		return t.Forced == f.Value.Bool
	}
	return false
}

// MediaSummary is compact media facts for a result row (§10.3 badges).
type MediaSummary struct {
	DurationMS *uint64  `json:"duration_ms"`
	Bitrate    *uint32  `json:"bitrate"`
	FPS        *float32 `json:"fps"`
	VideoCodec *string  `json:"video_codec"`
	HDR        []string `json:"hdr"`
	// Audio: one entry per audio track, e.g. "ac3 5.1 ar".
	Audio []string `json:"audio"`
	// Subtitles: languages (forced ones marked "en (forced)").
	Subtitles []string `json:"subtitles"`
}

// MediaSummary of an entry, or nil.
func (s *Store) MediaSummary(id uint32) *MediaSummary {
	m := s.media
	if !m.has(id) {
		return nil
	}
	r := m.row[id]
	out := &MediaSummary{HDR: []string{}, Audio: []string{}, Subtitles: []string{}}
	if d := uint64(m.durationMS[r]); d > 0 {
		out.DurationMS = &d
	}
	if b := m.bitrate[r]; b > 0 {
		out.Bitrate = &b
	}
	if f := m.fpsMilli[r]; f > 0 {
		v := float32(f) / 1000
		out.FPS = &v
	}
	for _, t := range m.tracks[r] {
		switch t.Kind {
		case media.TrackVideo:
			if out.VideoCodec == nil {
				c := t.Codec
				out.VideoCodec = &c
				out.HDR = append(out.HDR, hdrNames(t.HDR)...)
			}
		case media.TrackAudio:
			a := t.Codec
			if c := ChannelName(t.Channels); c != "" {
				a += " " + c
			} else if t.Channels > 0 {
				a += fmt.Sprintf(" %dch", t.Channels)
			}
			if t.Atmos {
				a += " atmos"
			}
			if t.Lang != "" {
				a += " " + t.Lang
			}
			out.Audio = append(out.Audio, a)
		case media.TrackSubtitle:
			l := t.Lang
			if l == "" {
				l = "?"
			}
			if t.Forced {
				l += " (forced)"
			}
			out.Subtitles = append(out.Subtitles, l)
		}
	}
	return out
}

// Quality is what the duplicate finder ranks copies by (§4.2).
type Quality struct {
	HDR        bool
	BPP        float32 // bits per pixel per frame (video)
	Channels   uint8   // most channels in any audio track
	Bitrate    uint32
	DurationMS uint32
	HasVideo   bool
}

// Quality of an entry's media, if any.
func (s *Store) Quality(id uint32) (Quality, bool) {
	m := s.media
	if !m.has(id) {
		return Quality{}, false
	}
	r := m.row[id]
	q := Quality{BPP: m.bpp[r], Bitrate: m.bitrate[r], DurationMS: m.durationMS[r]}
	for _, t := range m.tracks[r] {
		switch t.Kind {
		case media.TrackVideo:
			q.HasVideo = true
			q.HDR = q.HDR || t.HDR != 0
		case media.TrackAudio:
			q.Channels = max(q.Channels, t.Channels)
		}
	}
	return q, true
}

// Tracks are an entry's media tracks; nil when it has none.
func (s *Store) Tracks(id uint32) []media.Track {
	if !s.media.has(id) {
		return nil
	}
	return s.media.tracks[s.media.row[id]]
}

func (m *MediaIndex) memoryBytes() uint64 {
	n := uint64(len(m.durationMS))
	b := uint64(len(m.row))*4 + n*(4*7+1+4*3+8+3) + m.all.GetSizeInBytes()
	for _, ts := range m.tracks {
		b += uint64(len(ts))*120 + 24
	}
	for _, mm := range m.facets {
		for k, bm := range mm {
			b += uint64(len(k)) + bm.GetSizeInBytes() + 48
		}
	}
	for _, bm := range m.flags {
		b += bm.GetSizeInBytes()
	}
	return b
}
