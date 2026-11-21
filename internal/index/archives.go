package index

import (
	"archive/zip"
	"database/sql"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/jamalxcode/laserdot-go/internal/fold"
)

// Archive contents (the owner chose "search inside zip files"): the names
// of the files inside each zip on this Mac, read from the zip's central
// directory only (its table of contents at the end; nothing is
// decompressed). Kept in their own file next to the index (a cache rebuilt
// from the files) and in memory, where search matches them with the same
// regexes as file names (pillar 3).

// MaxArchiveNames: names kept for one archive (the first ones).
const MaxArchiveNames = 20_000

const archiveSchema = `
	CREATE TABLE IF NOT EXISTS archives (
		path TEXT PRIMARY KEY,
		size INTEGER NOT NULL,
		mtime INTEGER NOT NULL,
		-- the members' paths inside the archive, one per line ('' when unreadable)
		names TEXT NOT NULL
	);`

// IsArchiveExt: archives whose contents are listed (zip only: .docx, .epub
// and the like are zips too, but their insides are not files people name).
func IsArchiveExt(foldedExt string) bool { return foldedExt == "zip" }

// ArchiveDoc is one archive's listing.
type ArchiveDoc struct {
	Path  string
	Stamp DocStamp
	Names []string // paths inside the archive
}

type archiveMem struct {
	stamp  DocStamp
	names  []string // paths inside
	folded []string // folded base names, for matching
}

// ArchiveIndex holds the listings.
type ArchiveIndex struct {
	mu   sync.RWMutex
	conn *sql.DB
	mem  map[string]*archiveMem
}

// ListArchive reads a zip's member paths from its central directory
// (files only, not folders; at most MaxArchiveNames). ok is false when it
// is not a readable zip.
func ListArchive(p string) (names []string, ok bool) {
	defer func() {
		if recover() != nil {
			names, ok = nil, false
		}
	}()
	r, err := zip.OpenReader(p)
	if err != nil {
		return nil, false
	}
	defer r.Close()
	for _, f := range r.File {
		if len(names) == MaxArchiveNames {
			break
		}
		n := strings.ToValidUTF8(strings.ReplaceAll(f.Name, "\\", "/"), "?")
		if n == "" || strings.HasSuffix(n, "/") || strings.HasPrefix(path.Base(n), "._") || strings.HasPrefix(n, "__MACOSX/") {
			continue
		}
		names = append(names, strings.ReplaceAll(n, "\n", " "))
	}
	return names, true
}

// OpenArchives opens (or recreates, when damaged) the archive listings.
func OpenArchives(p string) (*ArchiveIndex, error) {
	a, err := tryOpenArchives(p)
	if err == nil {
		return a, nil
	}
	for _, s := range []string{"", "-wal", "-shm"} {
		os.Remove(p + s)
	}
	return tryOpenArchives(p)
}

func tryOpenArchives(p string) (*ArchiveIndex, error) {
	conn, err := sql.Open("sqlite", "file:"+p+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	var ok string
	if err := conn.QueryRow("PRAGMA quick_check").Scan(&ok); err != nil || ok != "ok" {
		conn.Close()
		if err == nil {
			err = fmt.Errorf("archive index damaged: %s", ok)
		}
		return nil, err
	}
	if _, err := conn.Exec(archiveSchema); err != nil {
		conn.Close()
		return nil, err
	}
	a := &ArchiveIndex{conn: conn, mem: map[string]*archiveMem{}}
	rows, err := conn.Query("SELECT path, size, mtime, names FROM archives")
	if err != nil {
		conn.Close()
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p, names string
		var size, mt int64
		if err := rows.Scan(&p, &size, &mt, &names); err != nil {
			conn.Close()
			return nil, err
		}
		a.mem[p] = memOf(DocStamp{uint64(size), mt}, splitNames(names))
	}
	if err := rows.Err(); err != nil {
		conn.Close()
		return nil, err
	}
	return a, nil
}

func splitNames(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func memOf(st DocStamp, names []string) *archiveMem {
	m := &archiveMem{stamp: st, names: names, folded: make([]string, len(names))}
	for i, n := range names {
		m.folded[i] = fold.Fold(path.Base(n))
	}
	return m
}

// Close closes the archive index.
func (a *ArchiveIndex) Close() error { return a.conn.Close() }

// Stamps of every listed archive, by path.
func (a *ArchiveIndex) Stamps() map[string]DocStamp {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[string]DocStamp, len(a.mem))
	for p, m := range a.mem {
		out[p] = m.stamp
	}
	return out
}

// Len: archives listed.
func (a *ArchiveIndex) Len() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.mem)
}

// Put stores (or replaces) listings.
func (a *ArchiveIndex) Put(docs []ArchiveDoc) error {
	if len(docs) == 0 {
		return nil
	}
	tx, err := a.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, d := range docs {
		if _, err := tx.Exec(`INSERT INTO archives (path, size, mtime, names) VALUES (?1, ?2, ?3, ?4)
			ON CONFLICT(path) DO UPDATE SET size = ?2, mtime = ?3, names = ?4`,
			d.Path, int64(d.Stamp.Size), d.Stamp.Modified, strings.Join(d.Names, "\n")); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range docs {
		a.mem[d.Path] = memOf(d.Stamp, d.Names)
	}
	return nil
}

// Remove drops listings.
func (a *ArchiveIndex) Remove(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	tx, err := a.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range paths {
		if _, err := tx.Exec("DELETE FROM archives WHERE path = ?", p); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range paths {
		delete(a.mem, p)
	}
	return nil
}

// Matching: for every archive with a member whose name matches every one of
// match (folded base names), the matching members' paths (up to limit
// each), by archive path; at most maxArchives archives.
func (a *ArchiveIndex) Matching(match func(folded string) bool, limit, maxArchives int) map[string][]string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := map[string][]string{}
	for p, m := range a.mem {
		if len(out) >= maxArchives {
			break
		}
		var found []string
		for i, f := range m.folded {
			if match(f) {
				found = append(found, m.names[i])
				if len(found) == limit {
					break
				}
			}
		}
		if len(found) > 0 {
			out[p] = found
		}
	}
	return out
}
