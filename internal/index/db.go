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

	"github.com/jamalxcode/laserdot-go/internal/fold"
	"github.com/jamalxcode/laserdot-go/internal/media"
	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// The durable store (spec §2.1.1): SQLite in WAL mode. It is the source of
// truth and the source for rebuilding the in-memory store; queries never
// touch it.
//
// Lifecycle safety (§2.6.4): the database is integrity-checked on open; a
// corrupt file is quarantined and recreated, and the list of indexed
// folders survives in a side file so they can be re-indexed.

// SchemaVersion: a different stored version drops and rebuilds the tables
// (the index is a rebuildable cache of the disk, §13.7).
const SchemaVersion = 2

// Probe codes stored per entry.
const (
	codeSkipped = 0
	codeOK      = 1
	codeFailed  = 2
	// codePending: a file still being written; probe once stable (§2.6.3).
	codePending = 3
)

// StableSecs: media files modified in the last 10 s are probed later.
const StableSecs = 10

// Probe is the outcome of reading one file's header.
type Probe struct {
	Status media.ProbeStatus
	Record *media.Record // when Status is ProbeOK
}

// Db is the durable store.
type Db struct {
	conn *sql.DB
	path string
	// mount: a drive index (§9). Paths are stored relative to this volume
	// root, and the file is untrusted input.
	mount string
}

// RootInfo describes an indexed folder.
type RootInfo struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
	// LastEventID: last applied FSEvents id (macOS), for replay on restart.
	LastEventID *uint64 `json:"last_event_id"`
	// LastVerified: Unix seconds when the folder was last seen.
	LastVerified int64 `json:"last_verified"`
	// Offline: missing at the last refresh; entries kept until purged.
	Offline bool `json:"offline"`
	// Complete: the first full index finished (false: interrupted, resumes
	// on the next refresh, §9.7).
	Complete bool `json:"complete"`
}

// OpenReport says what Open had to repair.
type OpenReport struct {
	// Quarantined: the corrupt file was moved here and a fresh index made.
	Quarantined string
	// RootsToReindex: the folders the corrupt index covered.
	RootsToReindex []string
}

// ErrLocked: a drive index is encrypted and the passphrase is missing or
// wrong.
var ErrLocked = errors.New("the drive index is encrypted: passphrase missing or wrong")

// UntrustedError: a drive index is damaged or not laserdot's own format
// (safe to rebuild if the drive is writable).
type UntrustedError struct{ Reason string }

func (e *UntrustedError) Error() string { return "the drive index can't be used: " + e.Reason }

func now() int64 { return time.Now().Unix() }

// Open opens (or creates) the index at path, checking its integrity first.
// A corrupt file is quarantined, never repaired in place.
func Open(path string) (*Db, OpenReport, error) {
	var report OpenReport
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, report, err
	}
	if _, err := os.Stat(path); err == nil {
		db, err := openConn(path, "")
		if err == nil {
			if err = db.check(); err == nil {
				return db, report, nil
			}
			db.Close()
		}
		q := fmt.Sprintf("%s.corrupt-%d", path, now())
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
	db, err := openConn(path, "")
	return db, report, err
}

// OpenDrive opens a drive's index (§9), treating it as untrusted: a
// schema whitelist, name validation on load, mount-relative paths. It never
// quarantines: the caller decides whether to rebuild.
func OpenDrive(path, mount string) (*Db, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if isEncryptedFile(path) {
		return nil, ErrLocked
	}
	db, err := openConn(path, filepath.Clean(mount))
	if err != nil {
		return nil, &UntrustedError{err.Error()}
	}
	if err := db.check(); err != nil {
		db.Close()
		return nil, &UntrustedError{err.Error()}
	}
	return db, nil
}

// isEncryptedFile: a non-empty file without the plain SQLite header.
func isEncryptedFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 16)
	n, _ := f.Read(head)
	return n > 0 && string(head[:n]) != "SQLite format 3\x00"[:n]
}

func openConn(path, mount string) (*Db, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)"
	if mount != "" {
		// A drive's index is input from outside this Mac: no trust.
		dsn += "&_pragma=trusted_schema(OFF)&_pragma=cell_size_check(ON)"
	}
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: one writer, and pragmas apply to it.
	conn.SetMaxOpenConns(1)
	db := &Db{conn: conn, path: path, mount: mount}
	if mount != "" {
		if err := db.verifySchema(); err != nil {
			conn.Close()
			return nil, err
		}
	}
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

// IsDrive: this is a drive's index.
func (d *Db) IsDrive() bool { return d.mount != "" }

func (d *Db) check() error {
	var res string
	if err := d.conn.QueryRow("PRAGMA quick_check").Scan(&res); err != nil {
		return err
	}
	if res != "ok" {
		return errors.New("integrity check failed: " + res)
	}
	return nil
}

// verifySchema: only laserdot's own tables and indexes may exist in a
// drive index — no triggers or views that would run on our writes.
func (d *Db) verifySchema() error {
	rows, err := d.conn.Query("SELECT type, name FROM sqlite_master")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, name string
		if err := rows.Scan(&kind, &name); err != nil {
			return err
		}
		ok := false
		switch kind {
		case "table":
			ok = name == "meta" || name == "roots" || name == "entries"
		case "index":
			ok = name == "entries_root" || name == "entries_parent" || strings.HasPrefix(name, "sqlite_autoindex_")
		}
		if !ok {
			return fmt.Errorf("unexpected %s `%s` in a drive index", kind, name)
		}
	}
	return rows.Err()
}

func (d *Db) migrate() error {
	if _, err := d.conn.Exec("CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		return err
	}
	v, err := d.Setting("schema_version")
	if err != nil {
		return err
	}
	if v != strconv.Itoa(SchemaVersion) {
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
		DELETE FROM meta WHERE key = 'snapshot';
		INSERT OR REPLACE INTO meta (key, value) VALUES ('schema_version', '` + strconv.Itoa(SchemaVersion) + `');`)
		if err != nil {
			return err
		}
	}
	if d.mount == "" {
		// Duplicate-finder cache (§4.1). Only in this Mac's own index: a
		// drive's index is untrusted, and a forged hash there could make a
		// different file look like a copy.
		_, err = d.conn.Exec(`CREATE TABLE IF NOT EXISTS fingerprints (
			key TEXT PRIMARY KEY, size INTEGER NOT NULL, mtime INTEGER NOT NULL,
			quick BLOB, full BLOB, image INTEGER, frames BLOB, flags INTEGER NOT NULL DEFAULT 0)`)
	}
	return err
}

// Setting is a stored setting ("" when unset).
func (d *Db) Setting(key string) (string, error) {
	var v string
	err := d.conn.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting stores a setting.
func (d *Db) SetSetting(key, value string) error {
	_, err := d.conn.Exec("INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)", key, value)
	return err
}

// SnapshotToken is the token of the snapshot that describes this database
// exactly (§2.1.1g); 0 when none. Every write to entries or roots clears
// it.
func (d *Db) SnapshotToken() (uint64, error) {
	v, err := d.Setting("snapshot")
	if err != nil || v == "" {
		return 0, err
	}
	t, _ := strconv.ParseUint(v, 10, 64)
	return t, nil
}

// SetSnapshotToken records that the snapshot written with token matches.
func (d *Db) SetSnapshotToken(token uint64) error {
	return d.SetSetting("snapshot", strconv.FormatUint(token, 10))
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// invalidateSnapshot: any change to entries or roots makes the snapshot
// stale. Every write below calls it in its transaction.
func invalidateSnapshot(tx execer) error {
	_, err := tx.Exec("DELETE FROM meta WHERE key = 'snapshot'")
	return err
}

// stored is a path as stored: relative to the volume root on a drive ("."
// is the root), absolute otherwise.
func (d *Db) stored(p string) string {
	if d.mount == "" {
		return p
	}
	rel, err := filepath.Rel(d.mount, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	if rel == "." {
		return "."
	}
	return "./" + rel
}

// unstored turns a stored path back into an absolute one. On a drive,
// anything that could point outside the volume (absolute paths, ..) is
// rejected.
func (d *Db) unstored(s string) (string, bool) {
	if d.mount == "" {
		return s, true
	}
	if s == "." {
		return d.mount, true
	}
	rel, ok := strings.CutPrefix(s, "./")
	if !ok || rel == "" {
		return "", false
	}
	for _, c := range strings.Split(rel, "/") {
		if !validName(c) {
			return "", false
		}
	}
	return filepath.Join(d.mount, rel), true
}

// validName: a drive index entry name must be a plain file name.
func validName(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.ContainsAny(n, "/\x00")
}

// sideFile lists the indexed folders outside the database.
func sideFile(path string) string { return path + ".roots" }

func (d *Db) writeSideFile() error {
	if d.mount != "" {
		return nil
	}
	roots, err := d.Roots()
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, r := range roots {
		b.WriteString(r.Path + "\n")
	}
	return os.WriteFile(sideFile(d.path), []byte(b.String()), 0o600)
}

// Roots are the indexed folders.
func (d *Db) Roots() ([]RootInfo, error) {
	rows, err := d.conn.Query("SELECT id, path, last_event_id, last_verified, offline, complete FROM roots ORDER BY path")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RootInfo{}
	for rows.Next() {
		var r RootInfo
		var ev sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Path, &ev, &r.LastVerified, &r.Offline, &r.Complete); err != nil {
			return nil, err
		}
		p, ok := d.unstored(r.Path)
		if !ok {
			continue
		}
		r.Path = p
		if ev.Valid {
			v := uint64(ev.Int64)
			r.LastEventID = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetEventID records the last applied FSEvents id of a root. A drive's
// index never stores one (event ids belong to one Mac).
func (d *Db) SetEventID(root string, id uint64) error {
	if d.mount != "" {
		return nil
	}
	_, err := d.conn.Exec("UPDATE roots SET last_event_id = ? WHERE path = ?", int64(id), d.stored(root))
	return err
}

// SetRootState records that root was seen (online) or missing at when.
func (d *Db) SetRootState(root string, online bool, when int64) error {
	var err error
	if online {
		_, err = d.conn.Exec("UPDATE roots SET offline = 0, last_verified = ? WHERE path = ?", when, d.stored(root))
	} else {
		_, err = d.conn.Exec("UPDATE roots SET offline = 1 WHERE path = ?", d.stored(root))
	}
	return err
}

// ShouldDefer: a media file that may still be being written; don't probe
// it yet (§2.6.3).
func ShouldDefer(e *scan.Entry) bool {
	return !e.IsDir && !e.Dataless && now()-e.Modified < StableSecs && pendingKind(e)
}

func pendingKind(e *scan.Entry) bool {
	ext := media.Extension(fold.Fold(filepath.Base(e.Path)))
	return media.WantsProbe(media.Classify(ext, e.IsDir, e.Size), ext)
}

func probeParts(p Probe) (code int, w, h uint32, blob []byte) {
	switch p.Status {
	case media.ProbeOK:
		if p.Record != nil {
			return codeOK, p.Record.Width, p.Record.Height, p.Record.Encode()
		}
	case media.ProbeFailed:
		return codeFailed, 0, 0, nil
	}
	return codeSkipped, 0, 0, nil
}

func storedCode(e *scan.Entry, p Probe) (int, uint32, uint32, []byte) {
	code, w, h, blob := probeParts(p)
	if p.Status == media.ProbeSkipped && ShouldDefer(e) {
		code = codePending
	}
	return code, w, h, blob
}

func tagsText(tags []string) any {
	if len(tags) == 0 {
		return nil
	}
	return strings.Join(tags, "\n")
}

const insertSQL = `INSERT INTO entries (id, root, parent, name, is_dir, is_symlink, dataless, size, mtime, ctime,
	inode, dev, probe, width, height, media, tags) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func insertRow(stmt *sql.Stmt, id, root int64, parent any, name string, e *scan.Entry, p Probe) error {
	code, w, h, blob := storedCode(e, p)
	_, err := stmt.Exec(id, root, parent, name, e.IsDir, e.IsSymlink, e.Dataless, int64(e.Size), e.Modified,
		e.Created, int64(e.Inode), int64(e.Dev), code, w, h, blob, tagsText(e.Tags))
	return err
}

// BeginRoot starts a first index of root: drops what was there (and any
// roots inside it) and records the root as incomplete. Returns the root row
// id and the base row id for its entries.
func (d *Db) BeginRoot(root string) (rootID, base int64, err error) {
	rs := d.stored(root)
	like := strings.TrimSuffix(rs, "/") + "/%"
	tx, err := d.conn.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	if err := invalidateSnapshot(tx); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec("DELETE FROM entries WHERE root IN (SELECT id FROM roots WHERE path = ?1 OR path LIKE ?2)", rs, like); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec("DELETE FROM roots WHERE path = ?1 OR path LIKE ?2", rs, like); err != nil {
		return 0, 0, err
	}
	res, err := tx.Exec("INSERT INTO roots (path, indexed_at, last_verified, complete) VALUES (?1, ?2, ?2, 0)", rs, now())
	if err != nil {
		return 0, 0, err
	}
	rootID, _ = res.LastInsertId()
	if err := tx.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM entries").Scan(&base); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return rootID, base, d.writeSideFile()
}

// InsertChunk commits entries[start:start+len(probes)]. Parents always come
// earlier, so every committed prefix is a valid tree: the checkpoint an
// interrupted index resumes from (§9.7).
func (d *Db) InsertChunk(rootID, base int64, entries []scan.Entry, start int, probes []Probe) error {
	rootName := d.stored(entries[0].Path)
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := invalidateSnapshot(tx); err != nil {
		return err
	}
	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for k, p := range probes {
		i := start + k
		e := &entries[i]
		var parent any
		name := filepath.Base(e.Path)
		if e.Parent < 0 {
			name = rootName
		} else {
			parent = base + int64(e.Parent)
		}
		if err := insertRow(stmt, base+int64(i), rootID, parent, name, e, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FinishRoot marks a root's first index complete.
func (d *Db) FinishRoot(root string) error {
	_, err := d.conn.Exec("UPDATE roots SET complete = 1 WHERE path = ?", d.stored(root))
	return err
}

// ReplaceRoot stores a walk of root (entries[0] is root) in one go.
func (d *Db) ReplaceRoot(root string, entries []scan.Entry, probes []Probe) error {
	if probes == nil {
		probes = make([]Probe, len(entries))
	}
	rootID, base, err := d.BeginRoot(root)
	if err != nil {
		return err
	}
	if err := d.InsertChunk(rootID, base, entries, 0, probes); err != nil {
		return err
	}
	return d.FinishRoot(root)
}

// ParentRef is the parent of an inserted row: an existing row, or an
// earlier insert of the same batch.
type ParentRef struct {
	Existing int64
	Inserted int // index of the insert, when Existing is 0
}

// OpKind is the kind of one write in an incremental batch.
type OpKind int

const (
	OpInsert OpKind = iota
	// OpUpdate: new metadata (and probe) for a changed file; keeps its id.
	OpUpdate
	// OpMove: rename and/or move; descendants follow.
	OpMove
	OpDelete
	OpSetModified
)

// Op is one write, applied in order in one transaction.
type Op struct {
	Kind     OpKind
	ID       int64 // update, move, set-modified
	IDs      []int64
	Parent   ParentRef // insert, move
	Root     int64     // insert, move
	Entry    *scan.Entry
	Probe    Probe
	Name     string // move
	Modified int64  // set-modified
}

// Apply runs an incremental batch in one transaction and returns the row
// ids of inserted entries, in order.
func (d *Db) Apply(ops []Op) ([]int64, error) {
	tx, err := d.conn.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := invalidateSnapshot(tx); err != nil {
		return nil, err
	}
	var next int64
	if err := tx.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM entries").Scan(&next); err != nil {
		return nil, err
	}
	ins, err := tx.Prepare(insertSQL)
	if err != nil {
		return nil, err
	}
	defer ins.Close()
	var inserted []int64
	parentOf := func(p ParentRef) int64 {
		if p.Existing != 0 {
			return p.Existing
		}
		return inserted[p.Inserted]
	}
	for i := range ops {
		op := &ops[i]
		switch op.Kind {
		case OpInsert:
			if err := insertRow(ins, next, op.Root, parentOf(op.Parent), filepath.Base(op.Entry.Path), op.Entry, op.Probe); err != nil {
				return nil, err
			}
			inserted = append(inserted, next)
			next++
		case OpUpdate:
			e := op.Entry
			code, w, h, blob := storedCode(e, op.Probe)
			if _, err := tx.Exec(`UPDATE entries SET size = ?, mtime = ?, ctime = ?, inode = ?, dev = ?, dataless = ?,
				probe = ?, width = ?, height = ?, media = ?, tags = ? WHERE id = ?`,
				int64(e.Size), e.Modified, e.Created, int64(e.Inode), int64(e.Dev), e.Dataless, code, w, h, blob,
				tagsText(e.Tags), op.ID); err != nil {
				return nil, err
			}
		case OpMove:
			if _, err := tx.Exec("UPDATE entries SET parent = ?, name = ? WHERE id = ?", parentOf(op.Parent), op.Name, op.ID); err != nil {
				return nil, err
			}
			// Moving across indexed folders: the subtree changes root.
			if _, err := tx.Exec(`WITH RECURSIVE sub(id) AS (SELECT ?1 UNION ALL
				SELECT e.id FROM entries e JOIN sub ON e.parent = sub.id)
				UPDATE entries SET root = ?2 WHERE id IN sub AND root != ?2`, op.ID, op.Root); err != nil {
				return nil, err
			}
		case OpDelete:
			del, err := tx.Prepare("DELETE FROM entries WHERE id = ?")
			if err != nil {
				return nil, err
			}
			for _, id := range op.IDs {
				if _, err := del.Exec(id); err != nil {
					del.Close()
					return nil, err
				}
			}
			del.Close()
		case OpSetModified:
			if _, err := tx.Exec("UPDATE entries SET mtime = ? WHERE id = ?", op.Modified, op.ID); err != nil {
				return nil, err
			}
		}
	}
	return inserted, tx.Commit()
}

// Identity is an entry's stored identity, for rename detection and probe
// reuse.
type Identity struct {
	Dev, Inode, Size uint64
	Modified         int64
	Probe            Probe
	Pending          bool
}

// Identities of rows by id (missing rows are left out).
func (d *Db) Identities(ids []int64) (map[int64]Identity, error) {
	out := make(map[int64]Identity, len(ids))
	stmt, err := d.conn.Prepare("SELECT dev, inode, size, mtime, probe, media FROM entries WHERE id = ?")
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	for _, id := range ids {
		var dev, inode, size int64
		var it Identity
		var code int
		var blob []byte
		err := stmt.QueryRow(id).Scan(&dev, &inode, &size, &it.Modified, &code, &blob)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		it.Dev, it.Inode, it.Size = uint64(dev), uint64(inode), uint64(size)
		it.Probe = decodeProbe(code, blob)
		it.Pending = code == codePending
		out[id] = it
	}
	return out, nil
}

func decodeProbe(code int, blob []byte) Probe {
	switch code {
	case codeOK:
		if r, err := media.Decode(blob); err == nil {
			return Probe{Status: media.ProbeOK, Record: r}
		}
	case codeFailed:
		return Probe{Status: media.ProbeFailed}
	}
	return Probe{}
}

// RootOf is the root row id of an entry (0 if unknown).
func (d *Db) RootOf(id int64) (int64, error) {
	var r int64
	err := d.conn.QueryRow("SELECT root FROM entries WHERE id = ?", id).Scan(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return r, err
}

// PriorProbe is a probe kept from an earlier run.
type PriorProbe struct {
	Size     uint64
	Modified int64
	Probe    Probe
}

// PriorProbes are the stored probe results under root, keyed by absolute
// path, so an unchanged file is never re-read (§2.1.2).
func (d *Db) PriorProbes(root string) (map[string]PriorProbe, error) {
	rs := d.stored(root)
	rows, err := d.conn.Query(`SELECT e.id, e.parent, e.name, e.is_dir, e.size, e.mtime, e.probe, e.media
		FROM entries e JOIN roots r ON r.id = e.root WHERE r.path = ?1 OR r.path LIKE ?2 ORDER BY e.id`,
		rs, strings.TrimSuffix(rs, "/")+"/%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type row struct {
		id     int64
		parent sql.NullInt64
		name   string
		isDir  bool
		size   int64
		mtime  int64
		code   int
		blob   []byte
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.parent, &r.name, &r.isDir, &r.size, &r.mtime, &r.code, &r.blob); err != nil {
			return nil, err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	paths := map[int64]string{}
	out := map[string]PriorProbe{}
	// Resolve paths in tree order (parents may have higher ids after moves).
	for len(all) > 0 {
		var next []row
		for _, r := range all {
			var p string
			if !r.parent.Valid {
				var ok bool
				if p, ok = d.unstored(r.name); !ok {
					continue
				}
			} else if pp, ok := paths[r.parent.Int64]; ok {
				p = filepath.Join(pp, r.name)
			} else {
				next = append(next, r)
				continue
			}
			if r.isDir {
				paths[r.id] = p
				continue
			}
			if r.code == codeOK || r.code == codeFailed {
				if pr := decodeProbe(r.code, r.blob); pr.Status != media.ProbeSkipped {
					out[p] = PriorProbe{Size: uint64(r.size), Modified: r.mtime, Probe: pr}
				}
			}
		}
		if len(next) == len(all) {
			break
		}
		all = next
	}
	return out, nil
}

// RemoveRoot forgets an indexed folder. False when it was not indexed.
func (d *Db) RemoveRoot(root string) (bool, error) {
	rs := d.stored(root)
	tx, err := d.conn.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := invalidateSnapshot(tx); err != nil {
		return false, err
	}
	if _, err := tx.Exec("DELETE FROM entries WHERE root IN (SELECT id FROM roots WHERE path = ?)", rs); err != nil {
		return false, err
	}
	res, err := tx.Exec("DELETE FROM roots WHERE path = ?", rs)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n > 0, d.writeSideFile()
}

// PurgeOffline drops offline roots not verified since before (§2.6.2:
// purge after 30 days) and returns them.
func (d *Db) PurgeOffline(before int64) ([]string, error) {
	roots, err := d.Roots()
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, r := range roots {
		if r.Offline && r.LastVerified < before {
			if _, err := d.RemoveRoot(r.Path); err != nil {
				return out, err
			}
			out = append(out, r.Path)
		}
	}
	return out, nil
}

// Load reads every entry in tree order (roots first, then breadth-first),
// ready for Build. On a drive, rows with unsafe names or paths are skipped.
func (d *Db) Load() ([]RawEntry, error) {
	rows, err := d.conn.Query(`SELECT id, parent, name, is_dir, is_symlink, dataless, size, mtime, ctime,
		width, height, media, tags, probe, inode FROM entries ORDER BY id`)
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
		var code int
		var blob []byte
		var tags sql.NullString
		if err := rows.Scan(&r.id, &r.parent, &r.e.Name, &r.e.IsDir, &r.e.IsSymlink, &r.e.Dataless, &size,
			&r.e.Modified, &r.e.Created, &r.e.Width, &r.e.Height, &blob, &tags, &code, &inode); err != nil {
			return nil, err
		}
		if !r.parent.Valid {
			p, ok := d.unstored(r.e.Name)
			if !ok {
				continue
			}
			r.e.Name = p
		} else if d.mount != "" && !validName(r.e.Name) {
			continue
		}
		r.e.Size, r.e.Inode, r.e.DBID = uint64(size), uint64(inode), r.id
		r.e.Pending = code == codePending
		if code == codeOK {
			if rec, err := media.Decode(blob); err == nil {
				r.e.Media = rec
			}
		}
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
		if _, seen := pos[r.id]; seen {
			continue // a cycle in a forged drive index
		}
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
