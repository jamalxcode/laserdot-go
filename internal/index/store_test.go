package index

import (
	"reflect"
	"slices"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/jamalxcode/laserdot-go/internal/media"
	"github.com/jamalxcode/laserdot-go/internal/query"
)

func re(parent int, name string, dir bool) RawEntry {
	return RawEntry{Parent: parent, Name: name, IsDir: dir}
}

func sampleStore() *Store {
	// Input deliberately not in DFS order: siblings interleaved.
	return Build([]RawEntry{
		re(-1, "/root", true), re(0, "a", true), re(0, "b", true),
		re(1, "a1.txt", false), re(2, "b1.txt", false), re(1, "a2.txt", false),
	})
}

func namesOf(s *Store, bm *roaring.Bitmap) []string {
	var v []string
	for _, id := range bm.ToArray() {
		v = append(v, s.Name(id))
	}
	slices.Sort(v)
	return v
}

func mustResolve(t *testing.T, s *Store, p string) uint32 {
	t.Helper()
	id, ok := s.Resolve(p)
	if !ok {
		t.Fatalf("%s not found", p)
	}
	return id
}

func TestPreorderResolveAndWithin(t *testing.T) {
	s := sampleStore()
	a := mustResolve(t, s, "/root/a")
	if got := namesOf(s, s.Within(a)); !reflect.DeepEqual(got, []string{"a1.txt", "a2.txt"}) {
		t.Error(got)
	}
	b1 := mustResolve(t, s, "/root/b/b1.txt")
	if s.Path(b1) != "/root/b/b1.txt" || s.FoldedPath(b1) != "/root/b/b1.txt" {
		t.Error(s.Path(b1))
	}
	if _, ok := s.Resolve("/root/zzz"); ok {
		t.Error("zzz")
	}
	if _, ok := s.Resolve("/elsewhere"); ok {
		t.Error("elsewhere")
	}
}

func TestAppendKillMove(t *testing.T) {
	s := sampleStore()
	a, b := mustResolve(t, s, "/root/a"), mustResolve(t, s, "/root/b")
	nw := s.Append(a, re(0, "a3.txt", false))
	if got := namesOf(s, s.Within(a)); !reflect.DeepEqual(got, []string{"a1.txt", "a2.txt", "a3.txt"}) {
		t.Error(got)
	}
	if id, _ := s.Resolve("/root/a/a3.txt"); id != nw {
		t.Error("resolve appended")
	}
	s.Kill(mustResolve(t, s, "/root/a/a1.txt"))
	if got := namesOf(s, s.Within(a)); !reflect.DeepEqual(got, []string{"a2.txt", "a3.txt"}) {
		t.Error(got)
	}
	s.RenameMove(a, b, "moved")
	if s.Path(nw) != "/root/b/moved/a3.txt" {
		t.Error(s.Path(nw))
	}
	if got := namesOf(s, s.Within(b)); !reflect.DeepEqual(got, []string{"a2.txt", "a3.txt", "b1.txt", "moved"}) {
		t.Error(got)
	}
	if n := s.Within(mustResolve(t, s, "/root")).GetCardinality(); n != 5 {
		t.Error(n)
	}
	if _, ok := s.Resolve("/root/a"); ok {
		t.Error("old path")
	}
	mustResolve(t, s, "/root/b/moved/a2.txt")
	if !s.TrigramCandidates("moved").Contains(a) {
		t.Error("renamed names are found through the trigram prefilter")
	}
	if s.Depth(nw) != 3 {
		t.Error("depth follows moves", s.Depth(nw))
	}
}

func TestTrigramCandidatesSuperset(t *testing.T) {
	s := Build([]RawEntry{re(-1, "/r", true), re(0, "Invoice.pdf", false), re(0, "voice.mp3", false)})
	if s.TrigramCandidates("invoice").GetCardinality() != 1 || s.TrigramCandidates("voice").GetCardinality() != 2 ||
		!s.TrigramCandidates("zzz").IsEmpty() || s.TrigramCandidates("in") != nil {
		t.Error("trigrams")
	}
}

func TestHiddenInheritsAndFollowsMoves(t *testing.T) {
	s := Build([]RawEntry{re(-1, "/r", true), re(0, ".config", true), re(1, "app.toml", false), re(0, "docs", true)})
	if s.hiddenBM.GetCardinality() != 2 {
		t.Fatal(s.hiddenBM.GetCardinality())
	}
	toml := mustResolve(t, s, "/r/.config/app.toml")
	s.RenameMove(toml, mustResolve(t, s, "/r/docs"), "app.toml")
	if s.hiddenBM.Contains(toml) {
		t.Error("still hidden")
	}
}

func run(t *testing.T, s *Store, q string) []string {
	t.Helper()
	pq, err := query.ParseAt(q, 0)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	c, err := query.Compile(pq, false)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	hits, err := s.Execute(c)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return namesOf(s, hits)
}

func movie() *media.Record {
	return &media.Record{Container: "mkv", DurationMS: 7_200_000, Width: 3840, Height: 2160, Bitrate: 40_000_000,
		Tracks: []media.Track{
			{Kind: media.TrackVideo, Codec: "hevc", Width: 3840, Height: 2160, FPSMilli: 23976, BitDepth: 10, HDR: media.HDRHDR10},
			{Kind: media.TrackAudio, Codec: "truehd", Lang: "en", Channels: 8, Atmos: true, Lossless: true, SampleRate: 48000},
			{Kind: media.TrackAudio, Codec: "ac3", Lang: "ar", Channels: 2, SampleRate: 48000},
			{Kind: media.TrackSubtitle, Codec: "subrip", Lang: "ar", Forced: true},
		}}
}

func TestMediaFiltersAndGroups(t *testing.T) {
	clip := &media.Record{Container: "mp4", DurationMS: 30_000, Width: 1920, Height: 1080,
		Tracks: []media.Track{{Kind: media.TrackVideo, Codec: "h264", Width: 1920, Height: 1080, FPSMilli: 60000, BitDepth: 8},
			{Kind: media.TrackAudio, Codec: "aac", Lang: "ar", Channels: 6}}}
	song := &media.Record{Container: "flac", DurationMS: 200_000, Tags: media.Tags{Artist: "Fairouz", Year: 1972},
		Tracks: []media.Track{{Kind: media.TrackAudio, Codec: "flac", Channels: 2, Lossless: true}}}
	photo := &media.Record{Container: "jpeg", Width: 3000, Height: 4000, Exif: &media.Exif{Camera: "Canon EOS R5", ISO: 3200, GPS: true}}
	s := Build([]RawEntry{
		re(-1, "/r", true),
		{Parent: 0, Name: "movie.mkv", Media: movie()}, {Parent: 0, Name: "movie.ar.srt"},
		{Parent: 0, Name: "clip.mp4", Media: clip}, {Parent: 0, Name: "song.flac", Media: song},
		{Parent: 0, Name: "IMG_1.jpg", Media: photo}, {Parent: 0, Name: "IMG_1.mov"},
		{Parent: 0, Name: "Screenshot 2024.png"},
	})
	for q, want := range map[string][]string{
		"res:4k":                          {"IMG_1.jpg", "movie.mkv"},
		"hdr:hdr10":                       {"movie.mkv"},
		"hdr:false":                       {"clip.mp4"},
		"codec:hevc":                      {"movie.mkv"},
		"lang:ar":                         {"clip.mp4", "movie.mkv"},
		"lang:ara":                        {"clip.mp4", "movie.mkv"},
		"a(lang:ar channels:5.1)":         {"clip.mp4"},
		"a(lang:en channels:7.1)":         {"movie.mkv"},
		"a(lang:ar atmos:true)":           {},
		"atmos:true":                      {"movie.mkv"},
		"lossless:true":                   {"movie.mkv", "song.flac"},
		"duration:>1h":                    {"movie.mkv"},
		"fps:>50":                         {"clip.mp4"},
		"bit_depth:10":                    {"movie.mkv"},
		"s.lang:ar forced:true":           {"movie.mkv"},
		"sidecar:true":                    {"movie.mkv"},
		"artist:fairouz year:1972":        {"song.flac"},
		"camera:canon iso:>1600 gps:true": {"IMG_1.jpg"},
		"orientation:portrait":            {"IMG_1.jpg"},
		"live:true":                       {"IMG_1.jpg", "IMG_1.mov"},
		"screenshot:true":                 {"Screenshot 2024.png"},
		"container:mkv,mp4":               {"clip.mp4", "movie.mkv"},
		"a.count:2":                       {"movie.mkv"},
		"ar:16:9":                         {"clip.mp4", "movie.mkv"},
	} {
		if want == nil {
			want = []string{}
		}
		got := run(t, s, q)
		if got == nil {
			got = []string{}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %q, want %q", q, got, want)
		}
	}
	sum := s.MediaSummary(mustResolve(t, s, "/r/movie.mkv"))
	if *sum.VideoCodec != "hevc" || !reflect.DeepEqual(sum.Audio, []string{"truehd 7.1 atmos en", "ac3 stereo ar"}) ||
		!reflect.DeepEqual(sum.Subtitles, []string{"ar (forced)"}) || !reflect.DeepEqual(sum.HDR, []string{"hdr10"}) {
		t.Errorf("%+v", sum)
	}
	if fv := s.FacetValues(query.FLang, query.Audio); len(fv) != 4 || fv[0].Value != "ar" || fv[0].Count != 2 {
		t.Errorf("%+v", fv)
	}
}

// Appended entries take part in pre-sorted orders.
func TestLargeSortMergesAppended(t *testing.T) {
	raw := []RawEntry{re(-1, "/r", true)}
	for i := 0; i < 21_000; i++ {
		raw = append(raw, RawEntry{Parent: 0, Name: "f" + itoa(i), Size: uint64(i)})
	}
	s := Build(raw)
	s.Append(0, RawEntry{Name: "big", Size: 1 << 40})
	s.Append(0, RawEntry{Name: "mid", Size: 20_999})
	pq, _ := query.ParseAt("in:/r", 0)
	c, _ := query.Compile(pq, false)
	hits, _ := s.Execute(c)
	var got []string
	for _, r := range s.Rank(hits, c, Page{Sort: BySize, Limit: 3}, 0) {
		got = append(got, s.Name(r.ID))
	}
	if !reflect.DeepEqual(got, []string{"big", "f20999", "mid"}) && !reflect.DeepEqual(got, []string{"big", "mid", "f20999"}) {
		t.Error(got)
	}
}
