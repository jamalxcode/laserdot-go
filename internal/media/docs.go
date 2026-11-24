package media

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// Documents and data files (0.9.49, the owner: "extract metadata … title and
// description, author/creator, software, copyright … data files: column
// names, data types and row count"). Like the media parsers, only what is
// needed is read: a data file's first 64 KB (whole only up to 8 MB, for an
// exact row count), an Office file's small property parts, a PDF's ends.

const (
	sampleBytes   = 64 << 10
	exactRowLimit = 8 << 20
	partLimit     = 1 << 20 // an Office XML part read for properties
)

// dataExts: data files whose columns and rows are read.
var dataExts = set("csv", "tsv", "tab", "psv", "json", "jsonl", "ndjson", "xlsx")

// officeExts: Office Open XML files whose document properties are read.
var officeExts = set("docx", "xlsx", "pptx", "docm", "xlsm", "pptm")

// IsDocExt: the extension has a document or data-file reader (not a media
// one): such files are read at once, never held back (index.ShouldDefer).
func IsDocExt(ext string) bool { return ext == "pdf" || dataExts[ext] || officeExts[ext] }

// DocExts are the document and data-file extensions whose details are read
// (0.9.49), sorted.
func DocExts() []string {
	var out []string
	for e := range dataExts {
		out = append(out, e)
	}
	for e := range officeExts {
		if !dataExts[e] {
			out = append(out, e)
		}
	}
	out = append(out, "pdf")
	sort.Strings(out)
	return out
}

func parseDoc(path, ext string, size uint64) *Record {
	rec := &Record{Container: ext}
	switch {
	case ext == "pdf":
		pdfInfo(path, size, rec)
	case officeExts[ext]:
		officeInfo(path, size, rec)
	}
	if dataExts[ext] && rec.Data == nil {
		rec.Data = dataInfo(path, ext, size)
	}
	if rec.Data == nil && rec.Tags == (Tags{}) && rec.Exif == nil {
		return nil
	}
	return rec
}

// ---------------------------------------------------------------- data files

func dataInfo(path, ext string, size uint64) *DataInfo {
	switch ext {
	case "xlsx":
		return nil // read with the Office properties (officeInfo)
	case "json":
		return jsonData(path, size)
	case "jsonl", "ndjson":
		return jsonLinesData(path, size)
	}
	return delimitedData(path, ext, size)
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	k, err := io.ReadFull(f, buf)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		err = nil
	}
	return buf[:k], err
}

// lineCount: newlines in the whole file (only called up to 8 MB).
func lineCount(path string) (uint64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	var n uint64
	buf := make([]byte, 64<<10)
	last := byte('\n')
	for {
		k, err := f.Read(buf)
		n += uint64(bytes.Count(buf[:k], []byte{'\n'}))
		if k > 0 {
			last = buf[k-1]
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, false
		}
	}
	if last != '\n' {
		n++ // a last line without its newline
	}
	return n, true
}

// rowsOf: data rows (lines minus the header), counted up to 8 MB, else
// estimated from the sample's average line length.
func rowsOf(path string, size uint64, sample []byte, header int) (uint64, bool) {
	if size <= exactRowLimit {
		if n, ok := lineCount(path); ok {
			return uint64(max(0, int64(n)-int64(header))), true
		}
	}
	lines := bytes.Count(sample, []byte{'\n'})
	if lines == 0 {
		return 0, false
	}
	est := size * uint64(lines) / uint64(len(sample))
	return uint64(max(0, int64(est)-int64(header))), false
}

func delimitedData(path, ext string, size uint64) *DataInfo {
	sample, err := readHead(path, sampleBytes)
	if err != nil || len(sample) == 0 {
		return nil
	}
	sample = bytes.TrimPrefix(sample, []byte("\xef\xbb\xbf"))
	firstLine := sample
	if i := bytes.IndexByte(sample, '\n'); i >= 0 {
		firstLine = sample[:i]
	}
	delim := ','
	switch ext {
	case "tsv", "tab":
		delim = '\t'
	case "psv":
		delim = '|'
	default:
		// European spreadsheets write ";".
		if bytes.Count(firstLine, []byte{';'}) > bytes.Count(firstLine, []byte{','}) {
			delim = ';'
		}
	}
	// Only whole lines of the sample (the last may be cut).
	body := sample
	if len(sample) == sampleBytes {
		if i := bytes.LastIndexByte(sample, '\n'); i > 0 {
			body = sample[:i+1]
		}
	}
	r := csv.NewReader(bytes.NewReader(body))
	r.Comma = delim
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	r.ReuseRecord = false
	header, err := r.Read()
	if err != nil || len(header) == 0 {
		return nil
	}
	var rows [][]string
	for len(rows) < 200 {
		rec, err := r.Read()
		if err != nil {
			break
		}
		rows = append(rows, rec)
	}
	d := &DataInfo{}
	for i, h := range header {
		if i >= maxColumns {
			break
		}
		d.Columns = append(d.Columns, cleanColumn(h, i))
		var vals []string
		for _, row := range rows {
			if i < len(row) {
				vals = append(vals, row[i])
			}
		}
		d.Types = append(d.Types, valueType(vals))
	}
	d.Rows, d.RowsExact = rowsOf(path, size, sample, 1)
	return d
}

func cleanColumn(h string, i int) string {
	h = strings.TrimSpace(strings.Trim(h, "\x00"))
	if h == "" {
		return "column " + strconv.Itoa(i+1)
	}
	if len(h) > 128 {
		h = h[:128]
	}
	return h
}

var dateLayouts = []string{"2006-01-02", "2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05",
	"01/02/2006", "02/01/2006", "1/2/2006", "2006/01/02", "02.01.2006", "Jan 2, 2006", "2 Jan 2006"}

// valueType: number, date, bool or text, from sample values (empty ones
// ignored); text when they disagree.
func valueType(vals []string) string {
	kind := ""
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		k := "text"
		lv := strings.ToLower(v)
		switch {
		case lv == "true" || lv == "false" || lv == "yes" || lv == "no":
			k = "bool"
		case isNumber(v):
			k = "number"
		case isDate(v):
			k = "date"
		}
		if kind == "" {
			kind = k
		} else if kind != k {
			return "text"
		}
	}
	if kind == "" {
		return "text"
	}
	return kind
}

func isNumber(v string) bool {
	v = strings.TrimPrefix(strings.TrimSuffix(v, "%"), "$")
	v = strings.ReplaceAll(v, ",", "")
	_, err := strconv.ParseFloat(v, 64)
	return err == nil
}

func isDate(v string) bool {
	for _, l := range dateLayouts {
		if _, err := time.Parse(l, v); err == nil {
			return true
		}
	}
	return false
}

func jsonType(v any) string {
	switch x := v.(type) {
	case float64, json.Number:
		return "number"
	case bool:
		return "bool"
	case string:
		if isDate(x) {
			return "date"
		}
		return "text"
	case nil:
		return ""
	}
	return "object"
}

// objectColumns: the keys of the first objects (in order) and their types.
func objectColumns(objs []map[string]any, order [][]string) *DataInfo {
	d := &DataInfo{}
	seen := map[string]int{}
	for _, keys := range order {
		for _, k := range keys {
			if _, ok := seen[k]; !ok && len(d.Columns) < maxColumns {
				seen[k] = len(d.Columns)
				d.Columns = append(d.Columns, cleanColumn(k, len(d.Columns)))
			}
		}
	}
	for _, c := range d.Columns {
		kind := ""
		for _, o := range objs {
			t := jsonType(o[c])
			if t == "" {
				continue
			}
			if kind == "" {
				kind = t
			} else if kind != t {
				kind = "text"
				break
			}
		}
		if kind == "" {
			kind = "text"
		}
		d.Types = append(d.Types, kind)
	}
	if len(d.Columns) == 0 {
		return nil
	}
	return d
}

// orderedKeys decodes one JSON object, keeping its keys' order.
func orderedKeys(dec *json.Decoder) (map[string]any, []string, bool) {
	t, err := dec.Token()
	if err != nil || t != json.Delim('{') {
		return nil, nil, false
	}
	obj := map[string]any{}
	var keys []string
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, nil, false
		}
		k, ok := kt.(string)
		if !ok {
			return nil, nil, false
		}
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, nil, false
		}
		obj[k] = v
		keys = append(keys, k)
	}
	if _, err := dec.Token(); err != nil { // }
		return nil, nil, false
	}
	return obj, keys, true
}

// jsonData: an array of objects (records): their keys, types and count.
func jsonData(path string, size uint64) *DataInfo {
	var src io.Reader
	if size <= exactRowLimit {
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		src = bufio.NewReader(f)
	} else {
		head, err := readHead(path, sampleBytes)
		if err != nil {
			return nil
		}
		src = bytes.NewReader(head)
	}
	dec := json.NewDecoder(src)
	if t, err := dec.Token(); err != nil || t != json.Delim('[') {
		return nil
	}
	var objs []map[string]any
	var order [][]string
	var n uint64
	for dec.More() {
		if len(objs) < 50 {
			o, keys, ok := orderedKeys(dec)
			if !ok {
				break
			}
			objs, order = append(objs, o), append(order, keys)
		} else {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				break
			}
		}
		n++
	}
	d := objectColumns(objs, order)
	if d == nil {
		return nil
	}
	d.Rows, d.RowsExact = n, size <= exactRowLimit
	if !d.RowsExact && n > 0 {
		d.Rows = size * n / sampleBytes // the sample held n of them
	}
	return d
}

func jsonLinesData(path string, size uint64) *DataInfo {
	sample, err := readHead(path, sampleBytes)
	if err != nil {
		return nil
	}
	var objs []map[string]any
	var order [][]string
	for _, line := range bytes.Split(sample, []byte{'\n'}) {
		if len(objs) >= 50 {
			break
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		o, keys, ok := orderedKeys(json.NewDecoder(bytes.NewReader(line)))
		if !ok {
			break
		}
		objs, order = append(objs, o), append(order, keys)
	}
	d := objectColumns(objs, order)
	if d == nil {
		return nil
	}
	d.Rows, d.RowsExact = rowsOf(path, size, sample, 0)
	return d
}

// ---------------------------------------------------------------- Office

func zipPart(z *zip.Reader, name string, limit int64) []byte {
	for _, f := range z.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil
		}
		defer rc.Close()
		b, _ := io.ReadAll(io.LimitReader(rc, limit))
		return b
	}
	return nil
}

// officeInfo: the document properties (docProps/core.xml and app.xml) of
// a Word, Excel or PowerPoint file, and an Excel file's first sheet as data.
func officeInfo(path string, size uint64, rec *Record) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	z, err := zip.NewReader(f, int64(size))
	if err != nil {
		return
	}
	var core struct {
		Title       string `xml:"title"`
		Subject     string `xml:"subject"`
		Creator     string `xml:"creator"`
		Description string `xml:"description"`
		Created     string `xml:"created"`
		Rights      string `xml:"rights"`
	}
	if b := zipPart(z, "docProps/core.xml", partLimit); b != nil {
		_ = xml.Unmarshal(b, &core)
	}
	var app struct {
		Application string `xml:"Application"`
		AppVersion  string `xml:"AppVersion"`
		Company     string `xml:"Company"`
	}
	if b := zipPart(z, "docProps/app.xml", partLimit); b != nil {
		_ = xml.Unmarshal(b, &app)
	}
	rec.Tags.Title = strings.TrimSpace(core.Title)
	rec.Tags.Author = strings.TrimSpace(core.Creator)
	rec.Tags.Description = strings.TrimSpace(core.Description)
	if rec.Tags.Description == "" {
		rec.Tags.Description = strings.TrimSpace(core.Subject)
	}
	rec.Tags.Software = strings.TrimSpace(app.Application)
	rec.Tags.Copyright = strings.TrimSpace(core.Rights)
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(core.Created)); err == nil {
		rec.Exif = &Exif{Taken: t.Unix(), HasTaken: true}
	}
	if strings.HasPrefix(rec.Container, "xls") {
		rec.Data = xlsxData(z)
	}
}

var (
	dimensionRE = regexp.MustCompile(`<dimension ref="[A-Z]+(\d+)(?::[A-Z]+(\d+))?"`)
	rowRE       = regexp.MustCompile(`(?s)<row[^>]*>(.*?)</row>`)
	cellRE      = regexp.MustCompile(`(?s)<c\b([^>]*?)(?:/>|>(.*?)</c>)`)
	valueRE     = regexp.MustCompile(`(?s)<v>(.*?)</v>`)
	inlineRE    = regexp.MustCompile(`(?s)<t[^>]*>(.*?)</t>`)
	attrTypeRE  = regexp.MustCompile(`\bt="([a-z]+)"`)
	sharedRE    = regexp.MustCompile(`(?s)<si>(.*?)</si>`)
)

// xlsxData: the first sheet's header row (its columns), the types of the
// rows under it, and its row count (from the sheet's stated dimension).
func xlsxData(z *zip.Reader) *DataInfo {
	sheet := zipPart(z, "xl/worksheets/sheet1.xml", 512<<10)
	if sheet == nil {
		return nil
	}
	var shared []string
	if b := zipPart(z, "xl/sharedStrings.xml", partLimit); b != nil {
		for _, m := range sharedRE.FindAllSubmatch(b, 20000) {
			var parts []string
			for _, t := range inlineRE.FindAllSubmatch(m[1], -1) {
				parts = append(parts, xmlText(t[1]))
			}
			shared = append(shared, strings.Join(parts, ""))
		}
	}
	cells := func(row []byte) (vals, types []string) {
		for _, c := range cellRE.FindAllSubmatch(row, maxColumns) {
			t := ""
			if m := attrTypeRE.FindSubmatch(c[1]); m != nil {
				t = string(m[1])
			}
			v := ""
			if m := valueRE.FindSubmatch(c[2]); m != nil {
				v = xmlText(m[1])
			} else if m := inlineRE.FindSubmatch(c[2]); m != nil {
				v = xmlText(m[1])
			}
			kind := "number"
			switch t {
			case "s":
				if i, err := strconv.Atoi(v); err == nil && i >= 0 && i < len(shared) {
					v = shared[i]
				}
				kind = "text"
			case "str", "inlineStr":
				kind = "text"
			case "b":
				kind = "bool"
			}
			if v == "" {
				kind = ""
			}
			vals, types = append(vals, v), append(types, kind)
		}
		return vals, types
	}
	rows := rowRE.FindAllSubmatch(sheet, 50)
	if len(rows) == 0 {
		return nil
	}
	header, _ := cells(rows[0][1])
	d := &DataInfo{}
	for i, h := range header {
		d.Columns = append(d.Columns, cleanColumn(h, i))
		kind := ""
		for _, r := range rows[1:] {
			_, types := cells(r[1])
			if i >= len(types) || types[i] == "" {
				continue
			}
			if kind == "" {
				kind = types[i]
			} else if kind != types[i] {
				kind = "text"
				break
			}
		}
		if kind == "" {
			kind = "text"
		}
		d.Types = append(d.Types, kind)
	}
	if m := dimensionRE.FindSubmatch(sheet); m != nil {
		last := m[1]
		if len(m[2]) > 0 {
			last = m[2]
		}
		if n, err := strconv.ParseUint(string(last), 10, 64); err == nil && n > 0 {
			d.Rows, d.RowsExact = n-1, true
		}
	} else {
		d.Rows, d.RowsExact = uint64(len(rows)-1), len(rows) < 50
	}
	if len(d.Columns) == 0 {
		return nil
	}
	return d
}

func xmlText(b []byte) string {
	r := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"", "&apos;", "'")
	return strings.TrimSpace(r.Replace(string(b)))
}

// ---------------------------------------------------------------- PDF

// pdfInfo: the document information dictionary (Title, Author, Subject,
// Creator, Producer, CreationDate), looked for in the file's last 256 KB
// and first 64 KB, where writers put it.
func pdfInfo(path string, size uint64, rec *Record) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	head := make([]byte, min(size, sampleBytes))
	if _, err := io.ReadFull(f, head); err != nil {
		return
	}
	if !bytes.HasPrefix(head, []byte("%PDF")) {
		return
	}
	var tail []byte
	if size > uint64(len(head)) {
		n := min(size-uint64(len(head)), 256<<10)
		tail = make([]byte, n)
		if _, err := f.ReadAt(tail, int64(size-n)); err != nil {
			tail = nil
		}
	}
	get := func(key string) string {
		for _, b := range [][]byte{tail, head} {
			if v := pdfValue(b, key); v != "" {
				return v
			}
		}
		return ""
	}
	rec.Tags.Title = get("Title")
	rec.Tags.Author = get("Author")
	rec.Tags.Description = get("Subject")
	rec.Tags.Software = get("Creator")
	if rec.Tags.Software == "" {
		rec.Tags.Software = get("Producer")
	}
	if c := get("CreationDate"); len(c) >= 16 && strings.HasPrefix(c, "D:") {
		if t, err := time.Parse("20060102150405", c[2:16]); err == nil {
			rec.Exif = &Exif{Taken: t.Unix(), HasTaken: true}
		}
	}
}

// pdfValue: the string after /Key — a literal (…) with escapes, or <hex>;
// UTF-16 when it starts with a byte-order mark.
func pdfValue(b []byte, key string) string {
	needle := []byte("/" + key)
	for start := 0; ; {
		i := bytes.Index(b[start:], needle)
		if i < 0 {
			return ""
		}
		i += start + len(needle)
		start = i
		if i < len(b) && (b[i] >= 'A' && b[i] <= 'z') {
			continue // a longer key (/Titles…)
		}
		for i < len(b) && (b[i] == ' ' || b[i] == '\r' || b[i] == '\n' || b[i] == '\t') {
			i++
		}
		if i >= len(b) {
			return ""
		}
		var raw []byte
		switch b[i] {
		case '(':
			depth := 0
			for j := i + 1; j < len(b) && len(raw) < 4096; j++ {
				c := b[j]
				if c == '\\' && j+1 < len(b) {
					j++
					switch b[j] {
					case 'n':
						raw = append(raw, '\n')
					case 'r', 't', 'b', 'f':
						raw = append(raw, ' ')
					case '(', ')', '\\':
						raw = append(raw, b[j])
					default:
						if b[j] >= '0' && b[j] <= '7' {
							k, v := j, 0
							for ; k < j+3 && k < len(b) && b[k] >= '0' && b[k] <= '7'; k++ {
								v = v*8 + int(b[k]-'0')
							}
							raw = append(raw, byte(v))
							j = k - 1
						}
					}
					continue
				}
				if c == '(' {
					depth++
				} else if c == ')' {
					if depth == 0 {
						break
					}
					depth--
				}
				raw = append(raw, c)
			}
		case '<':
			if i+1 < len(b) && b[i+1] == '<' {
				continue // a dictionary, not a string
			}
			end := bytes.IndexByte(b[i:], '>')
			if end < 0 || end > 8192 {
				continue
			}
			hex := strings.Map(func(r rune) rune {
				if strings.ContainsRune("0123456789abcdefABCDEF", r) {
					return r
				}
				return -1
			}, string(b[i+1:i+end]))
			for k := 0; k+1 < len(hex); k += 2 {
				v, _ := strconv.ParseUint(hex[k:k+2], 16, 8)
				raw = append(raw, byte(v))
			}
		default:
			continue
		}
		return strings.TrimSpace(pdfString(raw))
	}
}

func pdfString(raw []byte) string {
	if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
		u := make([]uint16, 0, len(raw)/2)
		for k := 2; k+1 < len(raw); k += 2 {
			u = append(u, uint16(raw[k])<<8|uint16(raw[k+1]))
		}
		return string(utf16.Decode(u))
	}
	return lossy(raw)
}
