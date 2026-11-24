package media

import (
	"archive/zip"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A CSV's columns, their types and its row count.
func TestCSVColumnsTypesRows(t *testing.T) {
	p := write(t, "sales.csv", "region,amount,date,paid\nnorth,1200.50,2024-03-01,yes\nsouth,\"1,300\",2024-03-02,no\neast,90,2024-03-03,yes\n")
	st, rec := Probe(p, Document, "csv", 120)
	if st != ProbeOK || rec.Data == nil {
		t.Fatalf("probe: %v %+v", st, rec)
	}
	d := rec.Data
	if !slices.Equal(d.Columns, []string{"region", "amount", "date", "paid"}) {
		t.Errorf("columns: %v", d.Columns)
	}
	if !slices.Equal(d.Types, []string{"text", "number", "date", "bool"}) {
		t.Errorf("types: %v", d.Types)
	}
	if d.Rows != 3 || !d.RowsExact {
		t.Errorf("rows: %d %v", d.Rows, d.RowsExact)
	}
	// Semicolons (European spreadsheets) and tabs.
	_, r2 := Probe(write(t, "eu.csv", "name;price\nA;1\n"), Document, "csv", 16)
	if r2 == nil || !slices.Equal(r2.Data.Columns, []string{"name", "price"}) {
		t.Errorf("semicolons: %+v", r2)
	}
	_, r3 := Probe(write(t, "t.tsv", "a\tb\n1\tx\n"), Document, "tsv", 10)
	if r3 == nil || !slices.Equal(r3.Data.Types, []string{"number", "text"}) {
		t.Errorf("tsv: %+v", r3)
	}
}

// JSON records and JSON Lines: keys in order, types, count.
func TestJSONRecords(t *testing.T) {
	_, r := Probe(write(t, "p.json", `[{"id":1,"name":"a","ok":true},{"id":2,"name":"b","ok":false}]`), Code, "json", 70)
	if r == nil || !slices.Equal(r.Data.Columns, []string{"id", "name", "ok"}) || r.Data.Rows != 2 ||
		!slices.Equal(r.Data.Types, []string{"number", "text", "bool"}) {
		t.Errorf("json: %+v", r)
	}
	_, l := Probe(write(t, "e.jsonl", "{\"ts\":\"2024-01-01\",\"v\":3}\n{\"ts\":\"2024-01-02\",\"v\":4}\n"), Code, "jsonl", 60)
	if l == nil || !slices.Equal(l.Data.Columns, []string{"ts", "v"}) || l.Data.Rows != 2 || l.Data.Types[0] != "date" {
		t.Errorf("jsonl: %+v", l)
	}
	if st, _ := Probe(write(t, "cfg.json", `{"a":1}`), Code, "json", 7); st != ProbeFailed {
		t.Errorf("a settings file is not data: %v", st)
	}
}

func zipFile(t *testing.T, name string, parts map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for n, body := range parts {
		w, _ := z.Create(n)
		w.Write([]byte(body))
	}
	z.Close()
	f.Close()
	return p
}

// Word: title, author, description, the app; Excel: also the first sheet.
func TestOfficeProperties(t *testing.T) {
	core := `<cp:coreProperties xmlns:cp="x" xmlns:dc="d" xmlns:dcterms="t"><dc:title>Budget 2024</dc:title>` +
		`<dc:creator>Jamal</dc:creator><dc:description>Yearly plan</dc:description>` +
		`<dcterms:created>2024-02-01T10:00:00Z</dcterms:created></cp:coreProperties>`
	app := `<Properties><Application>Microsoft Excel</Application></Properties>`
	sheet := `<worksheet><dimension ref="A1:C4"/><sheetData>` +
		`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="inlineStr"><is><t>Paid</t></is></c></row>` +
		`<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>12.5</v></c><c r="C2" t="b"><v>1</v></c></row>` +
		`</sheetData></worksheet>`
	shared := `<sst><si><t>Item</t></si><si><t>Cost</t></si><si><t>Tea</t></si></sst>`
	p := zipFile(t, "budget.xlsx", map[string]string{"docProps/core.xml": core, "docProps/app.xml": app,
		"xl/worksheets/sheet1.xml": sheet, "xl/sharedStrings.xml": shared})
	fi, _ := os.Stat(p)
	st, r := Probe(p, Document, "xlsx", uint64(fi.Size()))
	if st != ProbeOK {
		t.Fatalf("probe: %v", st)
	}
	if r.Tags.Title != "Budget 2024" || r.Tags.Author != "Jamal" || r.Tags.Description != "Yearly plan" ||
		r.Tags.Software != "Microsoft Excel" || r.Exif == nil || !r.Exif.HasTaken {
		t.Errorf("properties: %+v %+v", r.Tags, r.Exif)
	}
	if r.Data == nil || !slices.Equal(r.Data.Columns, []string{"Item", "Cost", "Paid"}) ||
		!slices.Equal(r.Data.Types, []string{"text", "number", "bool"}) || r.Data.Rows != 3 {
		t.Errorf("sheet: %+v", r.Data)
	}
	// It survives the index's encoding.
	back, err := Decode(r.Encode())
	if err != nil || back.Data == nil || back.Data.Rows != 3 || back.Tags.Author != "Jamal" {
		t.Errorf("round trip: %v %+v", err, back)
	}
}

// A PDF's information dictionary, literal and UTF-16 strings.
func TestPDFInfo(t *testing.T) {
	body := "%PDF-1.4\n1 0 obj\n<< /Title (Annual \\(Final\\) Report) /Author <FEFF004A0061006D0061006C> " +
		"/Creator (Pages) /CreationDate (D:20240315102233+03'00') >>\nendobj\n%%EOF\n"
	p := write(t, "r.pdf", body)
	st, r := Probe(p, Document, "pdf", uint64(len(body)))
	if st != ProbeOK || r.Tags.Title != "Annual (Final) Report" || r.Tags.Author != "Jamal" || r.Tags.Software != "Pages" ||
		r.Exif == nil || !r.Exif.HasTaken {
		t.Errorf("pdf: %v %+v %+v", st, r.Tags, r.Exif)
	}
}

// Version-1 records (indexed before 0.9.49) still decode.
func TestOldRecordsDecode(t *testing.T) {
	r := &Record{Container: "mp4", Tags: Tags{Title: "x"}}
	b := r.Encode()
	v1 := append([]byte{1}, b[1:]...)
	// strip the version-2 tail: description, author, copyright, software
	// (one byte each when empty), chapters (one), no data (one).
	v1 = v1[:len(v1)-6]
	back, err := Decode(v1)
	if err != nil || back.Tags.Title != "x" {
		t.Errorf("v1: %v %+v", err, back)
	}
}
