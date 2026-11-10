package index

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/fold"
	"github.com/jamalxcode/laserdot-go/internal/media"
	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// Incremental updates (spec §2.1.2, §2.6): apply "something changed under
// this path" signals without rebuilding.
//
//  1. Each change resolves to the deepest indexed folder (a new folder's
//     nearest indexed ancestor), which is rescanned — shallow, or the whole
//     subtree when events were dropped.
//  2. Disk is diffed against the index: added, removed, modified. A
//     same-named entry with a different inode was replaced.
//  3. New entries (including everything inside new folders) are paired
//     with removed entries (including everything inside removed folders) by
//     inode: a pair is a rename or move, applied in place, with no media
//     re-read (§2.6.1). A moved folder carries its contents along.
//  4. New and changed media files are probed in parallel, unless still
//     being written (§2.6.3) — those are marked pending.
//  5. One SQLite transaction, then the in-memory store is updated.

// UpdateStats reports an update batch. JSON names are the protocol's.
type UpdateStats struct {
	DirsScanned uint64 `json:"dirs_scanned"`
	Added       uint64 `json:"added"`
	Removed     uint64 `json:"removed"`
	// Renamed: renamed or moved in place, without re-reading media.
	Renamed     uint64 `json:"renamed"`
	Modified    uint64 `json:"modified"`
	Probed      uint64 `json:"probed"`
	ProbeFailed uint64 `json:"probe_failed"`
	// Pending: media files still being written; probed on a later pass.
	Pending uint64 `json:"pending"`
	// Reused: stored probes reused (moved with a new extension, unchanged).
	Reused uint64 `json:"reused"`
	// OfflineRoots: indexed folders found missing (kept, marked offline).
	OfflineRoots []string `json:"offline_roots"`
	// PendingPaths: files to re-check after the stability window.
	PendingPaths []string `json:"-"`
	MS           uint64   `json:"ms"`
}

// Merge adds o to u.
func (u *UpdateStats) Merge(o UpdateStats) {
	u.DirsScanned += o.DirsScanned
	u.Added += o.Added
	u.Removed += o.Removed
	u.Renamed += o.Renamed
	u.Modified += o.Modified
	u.Probed += o.Probed
	u.ProbeFailed += o.ProbeFailed
	u.Pending += o.Pending
	u.Reused += o.Reused
	u.OfflineRoots = append(u.OfflineRoots, o.OfflineRoots...)
	u.PendingPaths = append(u.PendingPaths, o.PendingPaths...)
}

type found struct {
	dir uint32
	e   scan.Entry
}

type diff struct {
	removed      []uint32
	added        []found // dir = parent folder
	modified     []found // dir = the entry
	dirMtimes    []found // dir = the folder, e.Modified = new time
	offlineRoots []uint32
	retarget     []uint32
	scanned      uint64
}

type action int

const (
	actInsert action = iota
	actMove          // same inode as a removed entry: rename/move it here
	actSkip          // inside a moved folder: comes along with it
)

// news is an entry on disk that the index does not have at that path.
type newEntry struct {
	parentStore uint32 // existing parent folder, or None
	parentNew   int    // or an earlier newEntry; -1
	e           scan.Entry
	act         action
	old         uint32 // for actMove
}

func extOf(name string) string { return media.Extension(fold.Fold(name)) }

// Apply applies changes to db and s.
func Apply(db *Db, s *Store, changes []scan.Change, opts scan.Options) (UpdateStats, error) {
	t := time.Now()
	stats, followups, err := applyOnce(db, s, changes, opts)
	if err != nil {
		return stats, err
	}
	// Moved folders whose contents can't be trusted are rescanned where they
	// now are — and those rescans can find more of the same (inode reuse
	// nested inside moved folders), so repeat until settled.
	for i := 0; i < 8 && len(followups) > 0; i++ {
		more, next, err := applyOnce(db, s, followups, opts)
		if err != nil {
			return stats, err
		}
		stats.Merge(more)
		followups = next
	}
	if len(followups) > 0 {
		more, _, err := applyOnce(db, s, followups, opts)
		if err != nil {
			return stats, err
		}
		stats.Merge(more)
	}
	stats.MS = uint64(time.Since(t).Milliseconds())
	return stats, nil
}

func hasPathPrefix(p, prefix string) bool { return within(p, prefix) }

func applyOnce(db *Db, s *Store, changes []scan.Change, opts scan.Options) (UpdateStats, []scan.Change, error) {
	stats := UpdateStats{}

	// 1. Changed paths → folders to rescan.
	type target struct {
		id        uint32
		recursive bool
	}
	var targets []target
	unresolvedAnchors := 0
	for _, c := range changes {
		p, recursive := filepath.Clean(c.Path), c.Recursive
		for {
			if id, ok := s.Resolve(p); ok {
				if p != filepath.Clean(c.Path) {
					unresolvedAnchors++
				}
				if s.IsDir(id) {
					targets = append(targets, target{id, recursive})
				} else if !s.IsRoot(id) {
					targets = append(targets, target{s.parent[id], false})
				}
				break
			}
			pp := filepath.Dir(p)
			if pp == p {
				break
			}
			p, recursive = pp, false
		}
	}
	var d diff
	done := map[uint32]bool{}
	for len(targets) > 0 {
		slices.SortFunc(targets, func(a, b target) int {
			if da, db := s.Depth(a.id), s.Depth(b.id); da != db {
				return int(da) - int(db)
			}
			if a.recursive != b.recursive {
				if a.recursive {
					return -1
				}
				return 1
			}
			return 0
		})
		var recursiveDone []uint32
		cur := targets
		targets = nil
		for _, tg := range cur {
			if !s.IsAlive(tg.id) || done[tg.id] {
				continue
			}
			covered := false
			for p := s.parent[tg.id]; p != None; p = s.parent[p] {
				if slices.Contains(recursiveDone, p) {
					covered = true
					break
				}
			}
			if covered {
				continue
			}
			done[tg.id] = true
			if tg.recursive {
				recursiveDone = append(recursiveDone, tg.id)
			}
			diffDir(s, tg.id, tg.recursive, opts, &d, done)
		}
		// Folders that vanished: rescan their parents.
		for _, r := range d.retarget {
			targets = append(targets, target{r, false})
		}
		d.retarget = nil
	}
	stats.DirsScanned = d.scanned

	// Offline roots keep their entries (§2.6.2).
	nowT := now()
	for _, r := range d.offlineRoots {
		p := s.Path(r)
		if err := db.SetRootState(p, false, nowT); err != nil {
			return stats, nil, err
		}
		stats.OfflineRoots = append(stats.OfflineRoots, p)
	}
	for _, c := range changes {
		if !c.Recursive {
			continue
		}
		if r, ok := s.Resolve(c.Path); ok && s.IsRoot(r) && !slices.Contains(d.offlineRoots, r) {
			if err := db.SetRootState(s.Path(r), true, nowT); err != nil {
				return stats, nil, err
			}
		}
	}

	// 2b. Everything new, with new folders expanded into their contents.
	var news []newEntry
	for _, a := range d.added {
		e := a.e
		if e.IsDir && !e.IsSymlink && !scan.IsOpaque(e.Path, opts) && !scan.IsMountPoint(e.Path) {
			if entries, _, err := scan.Walk(e.Path, opts); err == nil && len(entries) > 0 {
				base := len(news)
				for _, se := range entries {
					ne := newEntry{parentStore: None, parentNew: -1, e: se}
					if se.Parent < 0 {
						ne.parentStore = a.dir
					} else {
						ne.parentNew = base + se.Parent
					}
					news = append(news, ne)
				}
				stats.DirsScanned++
				continue
			}
		}
		news = append(news, newEntry{parentStore: a.dir, parentNew: -1, e: e})
	}

	// 3. Pair new entries with removed ones (and their contents) by inode.
	removed := slices.Clone(d.removed)
	slices.Sort(removed)
	removed = slices.Compact(removed)
	byInode := map[uint64]uint32{}
	// Volumes without stable inodes (FAT, exFAT) report 0: files there pair
	// by (name, size, mtime) instead.
	type nkey struct {
		name string
		size uint64
		mod  int64
	}
	byKey := map[nkey]uint32{}
	for _, id := range removed {
		for _, x := range append([]uint32{id}, s.Descendants(id)...) {
			if s.Inode(x) != 0 {
				byInode[s.Inode(x)] = x
			} else if !s.IsDir(x) {
				byKey[nkey{s.Name(x), s.Size(x), s.Modified(x)}] = x
			}
		}
	}
	type extChange struct {
		i   int
		old uint32
	}
	var extChanged []extChange
	for i := range news {
		n := &news[i]
		if n.parentNew >= 0 && news[n.parentNew].act != actInsert {
			n.act = actSkip
			continue
		}
		e := &n.e
		old, ok := uint32(0), false
		if e.Inode != 0 {
			old, ok = byInode[e.Inode]
		} else if !e.IsDir {
			k := nkey{filepath.Base(e.Path), e.Size, e.Modified}
			old, ok = byKey[k]
			delete(byKey, k)
		}
		if !ok || s.IsDir(old) != e.IsDir {
			continue
		}
		if e.IsDir || extOf(s.Name(old)) == extOf(filepath.Base(e.Path)) {
			delete(byInode, e.Inode)
			n.act, n.old = actMove, old
		} else {
			extChanged = append(extChanged, extChange{i, old})
		}
	}
	moved := map[uint32]bool{}
	for _, n := range news {
		if n.act == actMove {
			moved[n.old] = true
		}
	}
	// A file renamed with a new extension is re-added; keep its probe if the
	// content is unchanged.
	reuse := map[int]Probe{}
	if len(extChanged) > 0 {
		var ids []int64
		for _, x := range extChanged {
			ids = append(ids, s.DBID(x.old))
		}
		idents, err := db.Identities(ids)
		if err != nil {
			return stats, nil, err
		}
		for _, x := range extChanged {
			if id, ok := idents[s.DBID(x.old)]; ok {
				e := &news[x.i].e
				// Only a real read is worth reusing (a .txt renamed to .png
				// was never probed).
				if id.Size == e.Size && id.Modified == e.Modified && !id.Pending && id.Probe.Status != media.ProbeSkipped {
					reuse[x.i] = id.Probe
				}
			}
		}
	}
	// Removed subtrees, minus anything that moved out of them.
	var killed []uint32
	seen := map[uint32]bool{}
	for _, id := range removed {
		if moved[id] || !s.IsAlive(id) {
			continue
		}
		stack := []uint32{id}
		for len(stack) > 0 {
			x := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if moved[x] || seen[x] {
				continue
			}
			seen[x] = true
			killed = append(killed, x)
			if s.IsDir(x) {
				stack = append(stack, s.Children(x)...)
			}
		}
	}

	// 4. Probe new and changed files in parallel.
	moveChanged := func(old uint32, e *scan.Entry) bool {
		return !e.IsDir && (s.Size(old) != e.Size || s.Modified(old) != e.Modified || (s.IsPending(old) && !ShouldDefer(e)))
	}
	type job struct {
		newIdx int // or -1
		modIdx int
		e      *scan.Entry
	}
	var jobs []job
	for i := range news {
		n := &news[i]
		if n.act == actInsert || n.act == actMove && moveChanged(n.old, &n.e) {
			jobs = append(jobs, job{i, -1, &n.e})
		}
	}
	for i := range d.modified {
		jobs = append(jobs, job{-1, i, &d.modified[i].e})
	}
	probes := make([]Probe, len(jobs))
	var reusedHit []bool = make([]bool, len(jobs))
	parallelChunks(len(jobs), 8, func(k int) {
		j := jobs[k]
		e := j.e
		if e.IsDir || e.Dataless || e.IsSymlink || ShouldDefer(e) {
			return
		}
		ext := extOf(filepath.Base(e.Path))
		kind := media.Classify(ext, false, e.Size)
		if !media.WantsProbe(kind, ext) {
			return // e.g. a .png renamed to .txt
		}
		if j.newIdx >= 0 {
			if p, ok := reuse[j.newIdx]; ok {
				probes[k], reusedHit[k] = p, true
				return
			}
		}
		st, rec := media.Probe(e.Path, kind, ext, e.Size)
		probes[k] = Probe{Status: st, Record: rec}
	})
	newProbe := map[int]Probe{}
	modProbe := make([]Probe, len(d.modified))
	for k, j := range jobs {
		p := probes[k]
		switch {
		case reusedHit[k]:
			stats.Reused++
		case p.Status == media.ProbeOK:
			stats.Probed++
		case p.Status == media.ProbeFailed:
			stats.Probed++
			stats.ProbeFailed++
		case ShouldDefer(j.e):
			stats.Pending++
			stats.PendingPaths = append(stats.PendingPaths, j.e.Path)
		}
		if j.newIdx >= 0 {
			newProbe[j.newIdx] = p
		} else {
			modProbe[j.modIdx] = p
		}
	}

	// 5. Durable writes, one transaction. Order: deletes, inserts (parents
	// first), moves (may target inserted folders), updates.
	rootCache := map[int64]int64{}
	rootOf := func(parentDB int64) (int64, error) {
		if r, ok := rootCache[parentDB]; ok {
			return r, nil
		}
		r, err := db.RootOf(parentDB)
		rootCache[parentDB] = r
		return r, err
	}
	topParent := func(i int) uint32 {
		for news[i].parentNew >= 0 {
			i = news[i].parentNew
		}
		return news[i].parentStore
	}
	insertIndex := map[int]int{}
	var insertProbes []Probe
	var ops []Op
	if len(killed) > 0 {
		var ids []int64
		for _, x := range killed {
			ids = append(ids, s.DBID(x))
		}
		ops = append(ops, Op{Kind: OpDelete, IDs: ids})
	}
	parentRef := func(n *newEntry) ParentRef {
		if n.parentStore != None {
			return ParentRef{Existing: s.DBID(n.parentStore)}
		}
		return ParentRef{Inserted: insertIndex[n.parentNew]}
	}
	for i := range news {
		n := &news[i]
		if n.act != actInsert {
			continue
		}
		root, err := rootOf(s.DBID(topParent(i)))
		if err != nil {
			return stats, nil, err
		}
		ref := parentRef(n)
		insertIndex[i] = len(insertIndex)
		p := newProbe[i]
		insertProbes = append(insertProbes, p)
		ops = append(ops, Op{Kind: OpInsert, Parent: ref, Root: root, Entry: &n.e, Probe: p})
	}
	type moveUpdate struct {
		i int
		p Probe
	}
	var moveUpdates []moveUpdate
	for i := range news {
		n := &news[i]
		if n.act != actMove {
			continue
		}
		root, err := rootOf(s.DBID(topParent(i)))
		if err != nil {
			return stats, nil, err
		}
		ops = append(ops, Op{Kind: OpMove, ID: s.DBID(n.old), Parent: parentRef(n), Name: filepath.Base(n.e.Path), Root: root})
		if moveChanged(n.old, &n.e) {
			p := newProbe[i]
			ops = append(ops, Op{Kind: OpUpdate, ID: s.DBID(n.old), Entry: &n.e, Probe: p})
			moveUpdates = append(moveUpdates, moveUpdate{i, p})
		}
	}
	for i := range d.modified {
		ops = append(ops, Op{Kind: OpUpdate, ID: s.DBID(d.modified[i].dir), Entry: &d.modified[i].e, Probe: modProbe[i]})
	}
	for _, dm := range d.dirMtimes {
		ops = append(ops, Op{Kind: OpSetModified, ID: s.DBID(dm.dir), Modified: dm.e.Modified})
	}
	var newIDs []int64
	if len(ops) > 0 {
		var err error
		if newIDs, err = db.Apply(ops); err != nil {
			return stats, nil, err
		}
	}

	// 6. In-memory store, same order.
	touched := map[uint32]bool{}
	for _, id := range killed {
		touched[s.parent[id]] = true
		s.Kill(id)
		stats.Removed++
	}
	storeID := map[int]uint32{}
	k := 0
	for i := range news {
		n := &news[i]
		if n.act != actInsert {
			continue
		}
		parent := n.parentStore
		if parent == None {
			parent = storeID[n.parentNew]
		}
		id := s.Append(parent, rawOf(&n.e, insertProbes[k], newIDs[k]))
		k++
		storeID[i] = id
		touched[parent] = true
		stats.Added++
	}
	var followups []scan.Change
	for i := range news {
		n := &news[i]
		if n.act != actMove {
			continue
		}
		parent := n.parentStore
		if parent == None {
			parent = storeID[n.parentNew]
		}
		touched[s.parent[n.old]] = true
		touched[parent] = true
		// Verify a moved folder really is the same folder: inode numbers are
		// reused, so a folder deleted and another created in one batch can
		// share one. Its listing must match the moved entry's children;
		// otherwise rescan it where it now is. Also rescan when anything in
		// this batch happened inside it, at its old or new location (those
		// changes were reported at paths the index could not resolve yet).
		if n.e.IsDir {
			oldPath := s.Path(n.old)
			activity := unresolvedAnchors > 0
			for _, c := range changes {
				if activity {
					break
				}
				activity = hasPathPrefix(c.Path, oldPath) || hasPathPrefix(c.Path, n.e.Path) ||
					(c.Recursive && hasPathPrefix(n.e.Path, c.Path))
			}
			if activity || !sameChildren(s, n.old, n.e.Path, opts) {
				followups = append(followups, scan.Change{Path: n.e.Path, Recursive: true})
			}
		}
		s.RenameMove(n.old, parent, filepath.Base(n.e.Path))
		stats.Renamed++
	}
	// Moved files whose content also changed are replaced in memory.
	for _, mu := range moveUpdates {
		n := &news[mu.i]
		parent, dbID := s.parent[n.old], s.DBID(n.old)
		s.Kill(n.old)
		s.Append(parent, rawOf(&n.e, mu.p, dbID))
		stats.Modified++
	}
	for i := range d.modified {
		id := d.modified[i].dir
		parent, dbID := s.parent[id], s.DBID(id)
		s.Kill(id)
		s.Append(parent, rawOf(&d.modified[i].e, modProbe[i], dbID))
		touched[parent] = true
		stats.Modified++
	}
	for _, dm := range d.dirMtimes {
		s.SetModified(dm.dir, dm.e.Modified)
	}
	for dir := range touched {
		if dir != None && s.IsAlive(dir) {
			s.RepairSiblings(dir)
		}
	}
	return stats, followups, nil
}

func sameChildren(s *Store, dir uint32, path string, opts scan.Options) bool {
	list, err := scan.ListDir(path, opts)
	if err != nil {
		return false
	}
	var disk, indexed []string
	for _, e := range list {
		disk = append(disk, filepath.Base(e.Path))
	}
	for _, c := range s.Children(dir) {
		indexed = append(indexed, s.Name(c))
	}
	slices.Sort(disk)
	slices.Sort(indexed)
	return slices.Equal(disk, indexed)
}

func rawOf(e *scan.Entry, p Probe, dbID int64) RawEntry {
	r := RawEntry{
		Name: filepath.Base(e.Path), IsDir: e.IsDir, IsSymlink: e.IsSymlink, Dataless: e.Dataless,
		Size: e.Size, Modified: e.Modified, Created: e.Created, DBID: dbID, Tags: e.Tags, Inode: e.Inode,
		Pending: p.Status == media.ProbeSkipped && ShouldDefer(e),
	}
	if p.Status == media.ProbeOK && p.Record != nil {
		r.Media = p.Record
		r.Width, r.Height = p.Record.Width, p.Record.Height
	}
	return r
}

func diffDir(s *Store, dir uint32, recursive bool, opts scan.Options, out *diff, done map[uint32]bool) {
	path := s.Path(dir)
	if scan.IsOpaque(path, opts) || (!s.IsRoot(dir) && scan.IsMountPoint(path)) {
		return
	}
	list, err := scan.ListDir(path, opts)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || isNotDir(err) {
			if s.IsRoot(dir) {
				out.offlineRoots = append(out.offlineRoots, dir)
			} else {
				out.retarget = append(out.retarget, s.parent[dir])
			}
		}
		return // unreadable: leave as is
	}
	out.scanned++
	existing := map[string]uint32{}
	for _, c := range s.Children(dir) {
		existing[s.Name(c)] = c
	}
	for _, e := range list {
		name := filepath.Base(e.Path)
		id, ok := existing[name]
		delete(existing, name)
		switch {
		case !ok:
			out.added = append(out.added, found{dir, e})
		case s.IsDir(id) != e.IsDir || (e.IsDir && s.Inode(id) != e.Inode):
			// Different kind, or a folder replaced by another folder.
			out.removed = append(out.removed, id)
			out.added = append(out.added, found{dir, e})
		case e.IsDir:
			if s.Modified(id) != e.Modified {
				out.dirMtimes = append(out.dirMtimes, found{id, e})
			}
			if recursive && !e.IsSymlink {
				done[id] = true
				diffDir(s, id, true, opts, out, done)
			}
		default:
			if s.Size(id) != e.Size || s.Modified(id) != e.Modified || s.Inode(id) != e.Inode ||
				(s.IsPending(id) && !ShouldDefer(&e)) || tagsDiffer(s, id, e.Tags) {
				out.modified = append(out.modified, found{id, e})
			}
		}
	}
	for _, id := range existing {
		out.removed = append(out.removed, id)
	}
}

func isNotDir(err error) bool { return strings.Contains(err.Error(), "not a directory") }

func tagsDiffer(s *Store, id uint32, tags []string) bool {
	var cur []string
	for _, t := range tags {
		if t = fold.Fold(strings.TrimSpace(t)); t != "" {
			cur = append(cur, t)
		}
	}
	slices.Sort(cur)
	cur = slices.Compact(cur)
	var stored []string
	for t, bm := range s.tagBM {
		if bm.Contains(id) {
			stored = append(stored, t)
		}
	}
	slices.Sort(stored)
	return !slices.Equal(cur, stored)
}
