package index

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"

	"github.com/jamalxcode/laserdot-go/internal/fold"
)

// Content search (spec §2.4): the text of documents in chosen folders, in
// a contentless FTS5 table in its own file next to the index. Word matching
// is the one exception to regex-only matching (pillar 3).

// DefaultContentMaxBytes: larger files are not read.
const DefaultContentMaxBytes = 20 * 1000 * 1000

// MaxTextBytes: extracted text is cut here.
const MaxTextBytes = 4 * 1024 * 1024

const contentSchema = `
	CREATE TABLE IF NOT EXISTS docs (
		id INTEGER PRIMARY KEY,
		path TEXT NOT NULL UNIQUE,
		size INTEGER NOT NULL,
		mtime INTEGER NOT NULL,
		-- 1: text indexed; 0: skipped (binary, too large, unreadable)
		indexed INTEGER NOT NULL
	);
	CREATE VIRTUAL TABLE IF NOT EXISTS body USING fts5(
		text, content='', contentless_delete=1,
		tokenize='unicode61 remove_diacritics 0'
	);`

// ContentType is how a document's text is read.
type ContentType int

const (
	ContentNone ContentType = iota
	ContentText
	ContentPDF
)

var textExts = map[string]bool{}

func init() {
	for _, e := range strings.Fields(`txt text md markdown rst org adoc tex csv tsv json jsonl xml html htm xhtml
		css scss less yaml yml toml ini cfg conf log srt vtt ass rtf sh bash zsh fish py rb pl php lua r jl js mjs
		cjs ts tsx jsx vue svelte rs go c h cc cpp cxx hpp hh m mm swift kt kts java scala groovy gradle cs fs vb
		sql proto graphql dart ex exs erl hs ml clj lisp el vim zig nim asm s cmake make mk dockerfile tf nix bib
		plist strings xcconfig pbxproj gitignore env properties ipynb`) {
		textExts[e] = true
	}
}

// ContentTypeOf a folded extension.
func ContentTypeOf(foldedExt string) ContentType {
	switch {
	case foldedExt == "pdf":
		return ContentPDF
	case textExts[foldedExt]:
		return ContentText
	}
	return ContentNone
}

// ExtractText reads a document's text, folded (§2.5) and cut to
// MaxTextBytes; ok is false for binary or unreadable files.
func ExtractText(path string, kind ContentType) (string, bool) {
	var raw string
	var ok bool
	switch kind {
	case ContentText:
		raw, ok = readText(path)
	case ContentPDF:
		raw, ok = readPDF(path)
	}
	if !ok {
		return "", false
	}
	text := fold.Fold(raw)
	if len(text) > MaxTextBytes {
		cut := MaxTextBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
	}
	return text, true
}

// RawText reads a document's text as written (not folded), for showing a
// passage of it; ok is false for binary or unreadable files.
func RawText(path string, kind ContentType) (string, bool) {
	switch kind {
	case ContentText:
		return readText(path)
	case ContentPDF:
		return readPDF(path)
	}
	return "", false
}

func readText(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxTextBytes*2))
	if err != nil {
		return "", false
	}
	u16 := func(le bool) string {
		units := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			if le {
				units = append(units, uint16(b[i])|uint16(b[i+1])<<8)
			} else {
				units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
			}
		}
		return string(utf16.Decode(units))
	}
	switch {
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return u16(true), true
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return u16(false), true
	}
	if bytes.IndexByte(b[:min(len(b), 8192)], 0) >= 0 {
		return "", false
	}
	b = bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF"))
	s := strings.ToValidUTF8(string(b), "�")
	bad, n := 0, 0
	for _, r := range s {
		if n++; n > 8192 {
			break
		}
		if r == utf8.RuneError {
			bad++
		}
	}
	if bad > 64 {
		return "", false
	}
	return s, true
}

func readPDF(path string) (text string, ok bool) {
	// The PDF reader may panic on malformed files: a bad PDF is just
	// skipped.
	defer func() {
		if recover() != nil {
			text, ok = "", false
		}
	}()
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	rd, err := r.GetPlainText()
	if err != nil {
		return "", false
	}
	b, err := io.ReadAll(io.LimitReader(rd, MaxTextBytes*2))
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		return "", false
	}
	return string(b), true
}

// FTSQuery builds an FTS5 expression from words: each word's letter and
// digit runs become a quoted phrase (so no FTS syntax gets through),
// ANDed; with prefix, a final token of 3+ characters matches as a prefix.
func FTSQuery(words []string, prefix bool) (string, bool) {
	var parts []string
	for _, w := range words {
		toks := strings.FieldsFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
		if len(toks) == 0 {
			continue
		}
		phrase := `"` + strings.Join(toks, " ") + `"`
		if prefix && utf8.RuneCountInString(toks[len(toks)-1]) >= 3 {
			phrase += "*"
		}
		parts = append(parts, phrase)
	}
	return strings.Join(parts, " AND "), len(parts) > 0
}

// DocStamp identifies a document version.
type DocStamp struct {
	Size     uint64
	Modified int64
}

// Doc is a document to store; Indexed false records a skipped file.
type Doc struct {
	Path    string
	Stamp   DocStamp
	Text    string
	Indexed bool
}

// ContentIndex is the content database.
type ContentIndex struct {
	mu   sync.Mutex
	conn *sql.DB
	path string
}

// OpenContent opens (or recreates, when damaged) the content index: it is
// a cache, rebuilt from the files.
func OpenContent(path string) (*ContentIndex, error) {
	c, err := tryOpenContent(path)
	if err == nil {
		return c, nil
	}
	for _, s := range []string{"", "-wal", "-shm"} {
		os.Remove(path + s)
	}
	return tryOpenContent(path)
}

func tryOpenContent(path string) (*ContentIndex, error) {
	conn, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	var ok string
	if err := conn.QueryRow("PRAGMA quick_check").Scan(&ok); err != nil || ok != "ok" {
		conn.Close()
		if err == nil {
			err = fmt.Errorf("content index damaged: %s", ok)
		}
		return nil, err
	}
	if _, err := conn.Exec(contentSchema); err != nil {
		conn.Close()
		return nil, err
	}
	return &ContentIndex{conn: conn, path: path}, nil
}

// Close closes the content index.
func (c *ContentIndex) Close() error { return c.conn.Close() }

// Stamps of every stored document, by path.
func (c *ContentIndex) Stamps() (map[string]DocStamp, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.conn.Query("SELECT path, size, mtime FROM docs")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]DocStamp{}
	for rows.Next() {
		var p string
		var size, mt int64
		if err := rows.Scan(&p, &size, &mt); err != nil {
			return nil, err
		}
		out[p] = DocStamp{uint64(size), mt}
	}
	return out, rows.Err()
}

// Counts: documents indexed and skipped.
func (c *ContentIndex) Counts() (indexed, skipped uint64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	err = c.conn.QueryRow("SELECT COALESCE(SUM(indexed), 0), COALESCE(SUM(1 - indexed), 0) FROM docs").Scan(&indexed, &skipped)
	return
}

// Put stores (or replaces) documents in one transaction.
func (c *ContentIndex) Put(docs []Doc) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, err := c.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, d := range docs {
		var id int64
		err := tx.QueryRow("SELECT id FROM docs WHERE path = ?", d.Path).Scan(&id)
		if err == nil {
			if _, err := tx.Exec("DELETE FROM body WHERE rowid = ?", id); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := tx.QueryRow(`INSERT INTO docs (path, size, mtime, indexed) VALUES (?1, ?2, ?3, ?4)
			ON CONFLICT(path) DO UPDATE SET size = ?2, mtime = ?3, indexed = ?4 RETURNING id`,
			d.Path, int64(d.Stamp.Size), d.Stamp.Modified, d.Indexed).Scan(&id); err != nil {
			return err
		}
		if d.Indexed {
			if _, err := tx.Exec("INSERT INTO body (rowid, text) VALUES (?, ?)", id, d.Text); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Remove drops documents.
func (c *ContentIndex) Remove(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	tx, err := c.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range paths {
		var id int64
		err := tx.QueryRow("SELECT id FROM docs WHERE path = ?", p).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM body WHERE rowid = ?", id); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM docs WHERE id = ?", id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *ContentIndex) paths(q string, args ...any) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.conn.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Matching: every document whose text matches expr (for content:).
func (c *ContentIndex) Matching(expr string) ([]string, error) {
	return c.paths("SELECT d.path FROM body JOIN docs d ON d.id = body.rowid WHERE body MATCH ?", expr)
}

// Search: the best limit documents for expr, by BM25.
func (c *ContentIndex) Search(expr string, limit int) ([]string, error) {
	return c.paths("SELECT d.path FROM body JOIN docs d ON d.id = body.rowid WHERE body MATCH ? ORDER BY bm25(body) LIMIT ?", expr, limit)
}
