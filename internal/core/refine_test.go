package core

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func refineGroup(groups []RefineGroup, id string) []RefineOption {
	for _, g := range groups {
		if g.ID == id {
			return g.Options
		}
	}
	return nil
}

func tokens(opts []RefineOption) []string {
	var out []string
	for _, o := range opts {
		out = append(out, o.Token)
	}
	return out
}

// Searching for videos offers what the results have to choose from — the
// resolutions among them, sound or no sound, shape, length, codec — with
// counts; choosing one narrows the search to exactly that many.
func TestVideoResultsOfferTheirAttributes(t *testing.T) {
	ld, root := newCore(t)
	for name, file := range map[string]string{
		"clips/city 4k.mp4": "h264_4k.mp4", "clips/pq.mp4": "hevc10_pq.mp4", "clips/new.mp4": "av1.mp4",
		"clips/vertical.mov": "rotated.mov", "clips/old.mkv": "pal.mkv", "clips/talk.webm": "vp9.webm",
	} {
		writeAged(t, filepath.Join(root, name), corpus(t, file))
	}
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	groups, err := ld.Refinements("kind:video")
	if err != nil {
		t.Fatal(err)
	}
	res := refineGroup(groups, "resolution")
	if len(res) < 2 {
		t.Fatalf("several resolutions to choose from, got %v (all groups %v)", tokens(res), groups)
	}
	if !slices.Contains(tokens(res), "res:4k") {
		t.Errorf("the 4K clip's resolution is offered: %v", tokens(res))
	}
	if len(refineGroup(groups, "codec")) < 2 {
		t.Errorf("codecs offered: %v", tokens(refineGroup(groups, "codec")))
	}
	// Each count is what choosing it gives.
	for _, o := range append(res, refineGroup(groups, "codec")...) {
		r, err := ld.Search("kind:video "+o.Token, DefaultSearchOptions)
		if err != nil {
			t.Fatal(err)
		}
		if r.Total != o.Count {
			t.Errorf("%s: offered with %d, the search finds %d", o.Token, o.Count, r.Total)
		}
	}
	// What the query already asks for is not offered again; a choice that
	// would not narrow anything is left out.
	g2, _ := ld.Refinements("kind:video res:4k")
	if slices.Contains(tokens(refineGroup(g2, "resolution")), "res:4k") {
		t.Error("res:4k offered again")
	}
	one, _ := ld.Refinements("city")
	if len(refineGroup(one, "resolution")) > 0 {
		t.Error("a single result has nothing to choose between")
	}
}

// Documents offer their types; pictures and music their own attributes;
// nothing is offered for an empty query.
func TestDocumentResultsOfferTheirTypes(t *testing.T) {
	ld, root := newCore(t)
	write(t, root, "work/report.pdf", "%PDF-1.4")
	write(t, root, "work/report notes.txt", "x")
	write(t, root, "work/report.docx", "x")
	write(t, root, "work/report draft.pdf", "%PDF-1.4")
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	groups, err := ld.Refinements("report")
	if err != nil {
		t.Fatal(err)
	}
	types := tokens(refineGroup(groups, "type"))
	for _, want := range []string{"ext:pdf", "ext:txt", "ext:docx"} {
		if !slices.Contains(types, want) {
			t.Errorf("%s offered among %v", want, types)
		}
	}
	if g, _ := ld.Refinements(""); len(g) != 0 {
		t.Error("nothing for an empty query")
	}
}

// newCore: an empty index and a folder to fill.
func newCore(t *testing.T) (*Laserdot, string) {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	ld, err := Open(filepath.Join(dir, "data", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ld.Close() })
	root := filepath.Join(dir, "home")
	os.MkdirAll(root, 0o755)
	return ld, root
}
