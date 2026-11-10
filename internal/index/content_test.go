package index

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestFTSQueryQuotesTokensAndMakesPhrases(t *testing.T) {
	q := func(prefix bool, w ...string) string { s, _ := FTSQuery(w, prefix); return s }
	if got := q(true, "invoice", "march"); got != `"invoice"* AND "march"*` {
		t.Error(got)
	}
	if got := q(false, "2024-03"); got != `"2024 03"` {
		t.Error(got)
	}
	if got := q(false, `"; drop`); got != `"drop"` {
		t.Error(got)
	}
	if _, ok := FTSQuery([]string{"--"}, true); ok {
		t.Error("--")
	}
	if got := q(true, "ab"); got != `"ab"` {
		t.Error(got)
	}
}

func TestContentStoreSearchReplaceRemove(t *testing.T) {
	c, err := OpenContent(filepath.Join(t.TempDir(), "content.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a, b := "/x/a.txt", "/x/b.txt"
	st := DocStamp{1, 1}
	if err := c.Put([]Doc{{a, st, "quarterly report for march", true}, {b, st, "march march march madness", true}, {"/x/bin", st, "", false}}); err != nil {
		t.Fatal(err)
	}
	if i, s, _ := c.Counts(); i != 2 || s != 1 {
		t.Error(i, s)
	}
	q := func(s string) string { e, _ := FTSQuery([]string{s}, true); return e }
	if got, _ := c.Search(q("march"), 10); !reflect.DeepEqual(got, []string{b, a}) {
		t.Error(got)
	}
	if got, _ := c.Search(q("quart"), 10); !reflect.DeepEqual(got, []string{a}) {
		t.Error(got)
	}
	c.Put([]Doc{{a, DocStamp{2, 2}, "annual summary", true}})
	if got, _ := c.Search(q("quarterly"), 10); len(got) != 0 {
		t.Error(got)
	}
	if s, _ := c.Stamps(); s[a] != (DocStamp{2, 2}) {
		t.Error(s)
	}
	c.Remove([]string{b})
	if got, _ := c.Search(q("madness"), 10); len(got) != 0 {
		t.Error(got)
	}
	if i, s, _ := c.Counts(); i != 1 || s != 1 {
		t.Error(i, s)
	}
}
