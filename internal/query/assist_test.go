package query

import (
	"reflect"
	"strings"
	"testing"
)

func TestEveryFilterHasAnArabicDescription(t *testing.T) {
	for i := range Filters {
		if _, ok := arDescriptions[Filters[i].Key]; !ok {
			t.Errorf("filter `%s` has no Arabic description in i18n.go", Filters[i].Key)
		}
	}
	for k := range arDescriptions {
		if !strings.Contains(" "+keysOf()+" ", " "+k+" ") {
			t.Errorf("i18n.go has `%s`, which is not a filter", k)
		}
	}
}

func keysOf() string {
	var k []string
	for i := range Filters {
		k = append(k, Filters[i].Key)
	}
	return strings.Join(k, " ")
}

func TestLanguages(t *testing.T) {
	if LangFromCode("ar-SA") != Ar || LangFromCode("en_US") != En {
		t.Error("codes")
	}
	res := Lookup("res", NoScope)
	if Describe(res, En) != res.Description || !strings.Contains(Describe(res, Ar), "الدقة") || SectionTitle(SectionAudio, Ar) != "الصوت" {
		t.Error("describe")
	}
	if !strings.Contains(CheatSheetIn(Ar), "الدقة") || !strings.Contains(CheatSheet(), "res:") {
		t.Error("cheat sheet")
	}
}

func TestHintsFollowTheCursor(t *testing.T) {
	for _, c := range []struct {
		q    string
		cur  int
		want string
		pre  bool
	}{
		{"", 0, "res:4k", false}, {"invoice", 7, "add filters", false}, {"kind:video res:", 15, "res: sd 720p 1080p", true},
		{"chan", 4, "channels:", false}, {"res:4l", 6, "res:", true}, {"re:abc(", 7, "invalid regex", false},
		{"report*", 7, "whole name", false},
	} {
		h := Hint(c.q, c.cur, En)
		if c.pre && !strings.HasPrefix(h, c.want) || !c.pre && !strings.Contains(h, c.want) {
			t.Errorf("%q: %q", c.q, h)
		}
	}
}

func TestArabicHintsAndSections(t *testing.T) {
	if !strings.Contains(Hint("", 0, Ar), "فلاتر") {
		t.Error("empty")
	}
	if h := Hint("res:", 4, Ar); !strings.HasPrefix(h, "res: sd 720p") || !strings.Contains(h, "الدقة") {
		t.Error(h)
	}
	if !strings.HasPrefix(Hint("chan", 4, Ar), "فلاتر: channels:") {
		t.Error("chan")
	}
	for q, want := range map[string]Section{"kind:video chan": SectionAudio, "res:4k": SectionVideo, "s.": SectionSubtitles} {
		cur := len([]rune(q))
		if q == "res:4k" {
			cur = 2
		}
		if s, ok := SectionAt(q, cur); !ok || s != want {
			t.Errorf("%q: %v", q, s)
		}
	}
	if _, ok := SectionAt("invoice", 3); ok {
		t.Error("invoice")
	}
	if _, ok := SectionAt("x", 1); ok {
		t.Error("x")
	}
	if c := Completions("lossl", 5, Ar); !strings.Contains(c[0].Detail, "بلا فقد") {
		t.Error(c)
	}
}

func TestCompletesKeysAndValues(t *testing.T) {
	found := false
	for _, c := range Completions("kind:video re", 13, En) {
		found = found || c.Insert == "res:" && c.ReplaceStart == 11 && c.ReplaceEnd == 13
	}
	if !found {
		t.Error("res:")
	}
	var ins []string
	for _, c := range Completions("res:1", 5, En) {
		ins = append(ins, c.Insert)
	}
	if !reflect.DeepEqual(ins, []string{"res:1080p", "res:1440p"}) {
		t.Error(ins)
	}
	if c := Completions("kind:video,au", 13, En); c[0].Insert != "kind:video,audio" {
		t.Error(c)
	}
	if c := Completions("-a.lan", 6, En); c[0].Insert != "-a.lang:" {
		t.Error(c)
	}
	if len(Completions("hello world", 3, En)) != 0 {
		t.Error("words")
	}
	if fl := FilterList(Ar); len(fl) != len(Filters) || fl[0].SectionID != "File" {
		t.Error("filters")
	}
}
