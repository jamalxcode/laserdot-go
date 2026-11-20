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
	// After choosing a resolution every group stays: the chosen value comes
	// back selected, the other resolutions stay to switch to (counted
	// without the resolution chosen), and every other group is counted
	// within the chosen resolution — each count is exactly what choosing
	// it gives.
	g2, err := ld.Refinements("kind:video res:1080p")
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range groups {
		if len(refineGroup(g2, g.ID)) == 0 {
			t.Errorf("group %s disappeared after choosing a resolution", g.ID)
		}
	}
	var chosen *RefineOption
	for _, g := range g2 {
		for i, o := range g.Options {
			want := "kind:video res:1080p " + o.Token
			if g.ID == "resolution" {
				want = "kind:video " + o.Token // its own group: switching
				if o.Token == "res:1080p" {
					chosen = &g.Options[i]
				}
			}
			r, err := ld.Search(want, DefaultSearchOptions)
			if err != nil {
				t.Fatal(err)
			}
			if r.Total != o.Count {
				t.Errorf("%s %s: shows %d, choosing it gives %d", g.ID, o.Token, o.Count, r.Total)
			}
		}
	}
	if chosen == nil || !chosen.Selected {
		t.Fatalf("res:1080p comes back selected: %+v", refineGroup(g2, "resolution"))
	}
	if len(refineGroup(g2, "resolution")) < 2 {
		t.Error("the other resolutions stay to switch to")
	}
	// A choice that leaves a single result keeps every group too, so it
	// can be changed or taken out.
	g4, _ := ld.Refinements("kind:video res:4k")
	if len(refineGroup(g4, "resolution")) < 2 || len(g4) < len(groups)-1 {
		t.Errorf("with one result left, the choices stay: %v", g4)
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

// A choice typed by hand comes back selected as written (so the app can take
// it out again); quoted values and groups stay whole.
func TestTypedChoicesAreRecognised(t *testing.T) {
	ld, root := newCore(t)
	writeAged(t, filepath.Join(root, "a.mp4"), corpus(t, "h264_4k.mp4"))
	writeAged(t, filepath.Join(root, "b.mkv"), corpus(t, "pal.mkv"))
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	groups, _ := ld.Refinements("kind:video RES:4K")
	var found bool
	for _, o := range refineGroup(groups, "resolution") {
		if o.Selected && o.Token == "RES:4K" {
			found = true
		}
	}
	if !found {
		t.Errorf("RES:4K selected as written: %+v", refineGroup(groups, "resolution"))
	}
	got := splitTokens(`trip camera:"canon eos r5" a(lang:ar channels:5.1) -ext:pdf`)
	want := []string{"trip", `camera:"canon eos r5"`, "a(lang:ar channels:5.1)", "-ext:pdf"}
	if !slices.Equal(got, want) {
		t.Errorf("splitTokens: %q", got)
	}
	if refineGroupOf("-ext:pdf") != "" || refineGroupOf("ext:pdf") != "type" || refineGroupOf("trip") != "" {
		t.Error("only plain choice filters count as choices")
	}
}
