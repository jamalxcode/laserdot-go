package index

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite: no cgo (Issue 002)

	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// SchemaVersion: a different stored version drops and rebuilds the tables
// (the index is a cache of the disk; the side file says what to re-index).
const SchemaVersion = 1

// Probe states for media files (§2.6.3); G1 stores none.
const (
	ProbeNone    = 0
	ProbePending = 1
)

// Db is the durable store. SQLite is never queried at search time: Load
// feeds the in-memory Store, which answers queries.
type Db struct {
	conn *sql.DB
	path string
}

// RootInfo describes an indexed folder.
type RootInfo struct {
	Path      string
	IndexedAt int64
	Complete  bool
	Offline   bool
}

// OpenReport says what Open had to repair.
type OpenReport struct {
	// Quarantined: the corrupt file was moved here and a fresh index made.
	Quarantined string
	// RootsToReindex: the folders the corrupt index covered.
	RootsToReindex []string
}

// Open opens (or creates) the index at path, checking its integrity first.
// A corrupt file is quarantined, never repaired in place.
func Open(path string) (*Db, OpenReport, error) {
	var report OpenReport
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, report, err
	}
	if _, err := os.Stat(path); err == nil {
		db, err := openConn(path)
		if err == nil {
			err = db.check()
			if err != nil {
				db.Close()
			} else {
				return db, report, nil
			}
		}
		q := fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix())
		if err := os.Rename(path, q); err != nil {
			return nil, report, err
		}
		for _, ext := range []string{"-wal", "-shm"} {
			os.Remove(path + ext)
		}
		if b, err := os.ReadFile(sideFile(path)); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if l != "" {
					report.RootsToReindex = append(report.RootsToReindex, l)
				}
			}
		}
		report.Quarantined = q
	}
	db, err := openConn(path)
	return db, report, err
}

func openConn(path string) (*Db, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One writer; database/sql would otherwise open several connections.
	conn.SetMaxOpenConns(1)
	db := &Db{conn: conn, path: path}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, err
	}
	return db, nil
}

// Close closes the database.
func (d *Db) Close() error { return d.conn.Close() }

// Path of the database file.
func (d *Db) Path() string { return d.path }

func (d *Db) check() error {
	var res string
	if err := d.conn.QueryRow("PRAGMA quick_check").Scan(&res); err != nil {
		return err
	}
	if res != "ok" {
		return errors.New("integrity check: " + res)
	}
	return nil
}

func (d *Db) migrate() error {
	if _, err := d.conn.Exec("CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		return err
	}
	var v string
	err := d.conn.QueryRow("SELECT value FROM meta WHERE key = 'schema_version'").Scan(&v)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if v == strconv.Itoa(SchemaVersion) {
		return nil
	}
	_, err = d.conn.Exec(`
		DROP TABLE IF EXISTS entries;
		DROP TABLE IF EXISTS roots;
		CREATE TABLE roots (
			id INTEGER PRIMARY KEY,
			path TEXT NOT NULL UNIQUE,
			indexed_at INTEGER NOT NULL,
			last_event_id INTEGER,
			last_verified INTEGER NOT NULL,
			offline INTEGER NOT NULL DEFAULT 0,
			complete INTEGER NOT NULL DEFAULT 1
		);
		CREATE TABLE entries (
			id INTEGER PRIMARY KEY,
			root INTEGER NOT NULL,
			parent INTEGER,
			name TEXT NOT NULL,
			is_dir INTEGER NOT NULL,
			is_symlink INTEGER NOT NULL,
			dataless INTEGER NOT NULL,
			size INTEGER NOT NULL,
			mtime INTEGER NOT NULL,
			ctime INTEGER NOT NULL,
			inode INTEGER NOT NULL,
			dev INTEGER NOT NULL,
			probe INTEGER NOT NULL,
			width INTEGER NOT NULL,
			height INTEGER NOT NULL,
			media BLOB,
			tags TEXT
		);
		CREATE INDEX entries_root ON entries(root);
		CREATE INDEX entries_parent ON entries(parent);
		INSERT OR REPLACE INTO meta (key, value) VALUES ('schema_version', '` + strconv.Itoa(SchemaVersion) + `');`)
	return err
}

// sideFile lists the indexed folders outside the database, so a corrupt
// index can still say what to re-index.
func sideFile(path string) string { return path + ".roots" }

func (d *Db) writeSideFile() error {
	roots, err := d.Roots()
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, r := range roots {
		b.WriteString(r.Path)
		b.WriteByte('\n')
	}
	return os.WriteFile(sideFile(d.path), []byte(b.String()), 0o600)
}

// Roots are the indexed folders.
func (d *Db) Roots() ([]RootInfo, error) {
	rows, err := d.conn.Query("SELECT path, indexed_at, complete, offline FROM roots ORDER BY path")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RootInfo
	for rows.Next() {
		var r RootInfo
		if err := rows.Scan(&r.Path, &r.IndexedAt, &r.Complete, &r.Offline); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReplaceRoot stores a fresh walk of root (entries[0] is root itself),
// replacing what was indexed there and any indexed folders inside it.
func (d *Db) ReplaceRoot(root string, entries []scan.Entry) error {
	root = filepath.Clean(root)
	like := strings.TrimSuffix(root, "/") + "/%"
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if _, err := tx.Exec("DELETE FROM entries WHERE root IN (SELECT id FROM roots WHERE path = ?1 OR path LIKE ?2)", root, like); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM roots WHERE path = ?1 OR path LIKE ?2", root, like); err != nil {
		return err
	}
	res, err := tx.Exec("INSERT INTO roots (path, indexed_at, last_verified, complete) VALUES (?1, ?2, ?2, 1)", root, now)
	if err != nil {
		return err
	}
	rootID, _ := res.LastInsertId()
	var base int64
	if err := tx.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM entries").Scan(&base); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO entries
		(id, root, parent, name, is_dir, is_symlink, dataless, size, mtime, ctime, inode, dev, probe, width, height, media, tags)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, NULL, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, e := range entries {
		var parent any
		name := filepath.Base(e.Path)
		if e.Parent < 0 {
			name = root
		} else {
			parent = base + int64(e.Parent)
		}
		var tags any
		if len(e.Tags) > 0 {
			tags = strings.Join(e.Tags, "\n")
		}
		if _, err := stmt.Exec(base+int64(i), rootID, parent, name, e.IsDir, e.IsSymlink, e.Dataless,
			int64(e.Size), e.Modified, e.Created, int64(e.Inode), int64(e.Dev), ProbeNone, tags); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return d.writeSideFile()
}

// RemoveRoot forgets an indexed folder. False when it was not indexed.
func (d *Db) RemoveRoot(root string) (bool, error) {
	root = filepath.Clean(root)
	tx, err := d.conn.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM entries WHERE root IN (SELECT id FROM roots WHERE path = ?)", root); err != nil {
		return false, err
	}
	res, err := tx.Exec("DELETE FROM roots WHERE path = ?", root)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n > 0, d.writeSideFile()
}

// Load reads every entry in tree order (roots first, then breadth-first),
// ready for Build.
func (d *Db) Load() ([]RawEntry, error) {
	rows, err := d.conn.Query(`SELECT id, parent, name, is_dir, is_symlink, dataless, size, mtime, ctime,
		width, height, tags, probe, inode FROM entries ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type row struct {
		id     int64
		parent sql.NullInt64
		e      RawEntry
	}
	var all []row
	for rows.Next() {
		var r row
		var size, inode int64
		var probe int
		var tags sql.NullString
		if err := rows.Scan(&r.id, &r.parent, &r.e.Name, &r.e.IsDir, &r.e.IsSymlink, &r.e.Dataless, &size,
			&r.e.Modified, &r.e.Created, &r.e.Width, &r.e.Height, &tags, &probe, &inode); err != nil {
			return nil, err
		}
		r.e.Size, r.e.Inode, r.e.DBID = uint64(size), uint64(inode), r.id
		r.e.Pending = probe == ProbePending
		if tags.Valid && tags.String != "" {
			r.e.Tags = strings.Split(tags.String, "\n")
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	kids := map[int64][]int{}
	var queue []int
	for i, r := range all {
		if r.parent.Valid {
			kids[r.parent.Int64] = append(kids[r.parent.Int64], i)
		} else {
			queue = append(queue, i)
		}
	}
	out := make([]RawEntry, 0, len(all))
	pos := make(map[int64]int, len(all))
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		r := all[i]
		r.e.Parent = -1
		if r.parent.Valid {
			r.e.Parent = pos[r.parent.Int64]
		}
		pos[r.id] = len(out)
		out = append(out, r.e)
		queue = append(queue, kids[r.id]...)
	}
	return out, nil
}
