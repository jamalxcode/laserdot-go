package core

import (
	"archive/zip"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"

	_ "modernc.org/sqlite"
)

// 0.9.49: details written inside documents and data files are searchable.
func TestDocumentAndDataDetails(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	lib := filepath.Join(base, "lib")
	writeAged(t, filepath.Join(lib, "sales.csv"),
		[]byte("region,amount,date,paid\nnorth,12.5,2024-01-02,true\nsouth,3,2024-02-03,false\neast,7,2024-03-04,true\n"))
	writeAged(t, filepath.Join(lib, "people.json"), []byte(`[{"name":"a","age":3},{"name":"b","age":4}]`))
	writeAged(t, filepath.Join(lib, "report.pdf"), []byte("%PDF-1.4\n1 0 obj\n<< /Title (Annual Report) /Author (Jamal) "+
		"/Subject (Yearly wedding budget) /Producer (Pages) >>\nendobj\n%%EOF\n"))
	writeAged(t, filepath.Join(lib, "plan.docx"), docx(t, `<cp:coreProperties xmlns:cp="x" xmlns:dc="d">`+
		`<dc:title>Plan</dc:title><dc:creator>Sara</dc:creator><dc:rights>(c) Sala</dc:rights></cp:coreProperties>`))
	writeAged(t, filepath.Join(lib, "notes.txt"), []byte("region amount"))
	ld, _ := Open(filepath.Join(base, "i.db"))
	defer ld.Close()
	ld.Index(lib)
	expect(t, lnames(t, ld, "column:region"), "sales.csv")
	expect(t, lnames(t, ld, "column:age"), "people.json")
	expect(t, lnames(t, ld, "rows:3"), "sales.csv")
	expect(t, lnames(t, ld, "rows:>1"), "people.json", "sales.csv")
	expect(t, lnames(t, ld, "columns:4"), "sales.csv")
	expect(t, lnames(t, ld, "coltype:date"), "sales.csv")
	expect(t, lnames(t, ld, "coltype:number"), "people.json", "sales.csv")
	expect(t, lnames(t, ld, "author:jamal"), "report.pdf")
	expect(t, lnames(t, ld, "author:sara"), "plan.docx")
	expect(t, lnames(t, ld, "desc:*wedding*"), "report.pdf")
	expect(t, lnames(t, ld, "software:pages"), "report.pdf")
	expect(t, lnames(t, ld, "copyright:*sala*"), "plan.docx")
	expect(t, lnames(t, ld, `title:"annual report"`), "report.pdf")
}

func docx(t *testing.T, core string) []byte {
	t.Helper()
	p := filepath.Join(t.TempDir(), "d.docx")
	f, _ := os.Create(p)
	z := zip.NewWriter(f)
	w, _ := z.Create("docProps/core.xml")
	w.Write([]byte(core))
	z.Close()
	f.Close()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// An index built before 0.9.49 reads its documents again, once, on open.
func TestOlderIndexReadsNewDetails(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	lib := filepath.Join(base, "lib")
	writeAged(t, filepath.Join(lib, "sales.csv"), []byte("region,amount\nnorth,1\n"))
	dbPath := filepath.Join(base, "i.db")
	ld, _ := Open(dbPath)
	ld.Index(lib)
	ld.Close()
	// What an older Laserdot left: the file never read, an older version mark.
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"UPDATE entries SET probe = 0, media = NULL WHERE name = 'sales.csv'",
		"UPDATE meta SET value = '1' WHERE key = 'details_version'", "DELETE FROM meta WHERE key = 'snapshot'"} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	conn.Close()
	ld, _ = Open(dbPath)
	defer ld.Close()
	ld.background.Wait()
	expect(t, lnames(t, ld, "column:region"), "sales.csv")
}

// Data files offer their column names and sizes, documents their authors.
func TestDocumentDetailsAreChoices(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	lib := filepath.Join(base, "lib")
	writeAged(t, filepath.Join(lib, "sales 2024.csv"), []byte("region,amount\nnorth,1\nsouth,2\n"))
	writeAged(t, filepath.Join(lib, "sales report.pdf"), []byte("%PDF-1.4\n<< /Author (Jamal) /Producer (Pages) >>\n%%EOF\n"))
	ld, _ := Open(filepath.Join(base, "i.db"))
	defer ld.Close()
	ld.Index(lib)
	groups, err := ld.Refinements("sales")
	if err != nil {
		t.Fatal(err)
	}
	for g, want := range map[string]string{"column": "column:region", "rows": "rows:<100", "author": "author:jamal", "software": "software:pages"} {
		if got := tokens(refineGroup(groups, g)); !slices.Contains(got, want) {
			t.Errorf("%s: %s offered among %v", g, want, got)
		}
	}
	// A chosen author narrows the results and shows as chosen.
	expect(t, lnames(t, ld, "sales author:jamal"), "sales report.pdf")
}

// Files written a moment ago (the self-test's case): a JSON file — a "code"
// kind — is read at once like a CSV, both offer their columns, and a size
// every result shares is no choice.
func TestFreshDataFilesAreReadAtOnce(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	lib := filepath.Join(base, "lib")
	os.MkdirAll(lib, 0o755)
	os.WriteFile(filepath.Join(lib, "sales-figures.csv"), []byte("region,amount\nnorth,1\nsouth,2\n"), 0o644)
	os.WriteFile(filepath.Join(lib, "sales-targets.json"), []byte(`[{"month":"jan","target":10}]`), 0o644)
	ld, _ := Open(filepath.Join(base, "i.db"))
	defer ld.Close()
	ld.Index(lib)
	expect(t, lnames(t, ld, "column:month"), "sales-targets.json")
	groups, _ := ld.Refinements("sales")
	cols := tokens(refineGroup(groups, "column"))
	if !slices.Contains(cols, "column:region") || !slices.Contains(cols, "column:month") {
		t.Errorf("columns of both files: %v", cols)
	}
	if r := refineGroup(groups, "rows"); len(r) != 0 {
		t.Errorf("both are small: nothing to choose between (%v)", tokens(r))
	}
}
