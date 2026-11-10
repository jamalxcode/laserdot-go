// Package index is the durable store (SQLite), the in-memory store that
// answers queries (spec §2.1.1a–f), live updates, and query execution and
// ranking.
package index

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/jamalxcode/laserdot-go/internal/fold"
	"github.com/jamalxcode/laserdot-go/internal/media"
	"github.com/jamalxcode/laserdot-go/internal/query"
)

// None marks "no entry" (no parent).
const None = ^uint32(0)

// Flag bits per entry.
const (
	FlagDir      uint8 = 1
	FlagHidden   uint8 = 2
	FlagSymlink  uint8 = 4
	FlagDataless uint8 = 8
)

// RawEntry is an input row for Build and Append.
type RawEntry struct {
	// Parent is the index of the parent in the input slice; -1 for a root
	// folder, whose Name is its absolute path. Append takes the parent id
	// separately.
	Parent    int
	Name      string
	IsDir     bool
	IsSymlink bool
	Dataless  bool
	Size      uint64
	Modified  int64
	Created   int64
	// Width and Height: display dimensions, 0 when unknown (taken from
	// Media when unset).
	Width, Height uint32
	// Media: the Tier 1 record, when the file was probed successfully.
	Media *media.Record
	DBID  int64
	Tags  []string
	// Pending: a media file still being written; probed later (§2.6.3).
	Pending bool
	// Inode: file identity. A same-named entry with a different inode was
	// replaced; a removed + added pair with the same inode was moved.
	Inode uint64
}

// arena stores strings in one growing buffer, so a rename only appends.
type arena struct {
	buf   []byte
	start []uint32
	n     []uint32
}

func (a *arena) push(s string) {
	a.start = append(a.start, uint32(len(a.buf)))
	a.n = append(a.n, uint32(len(s)))
	a.buf = append(a.buf, s...)
}

func (a *arena) set(i uint32, s string) {
	a.start[i] = uint32(len(a.buf))
	a.n[i] = uint32(len(s))
	a.buf = append(a.buf, s...)
}

func (a *arena) get(i uint32) string {
	s := a.start[i]
	return string(a.buf[s : s+a.n[i]])
}

// bytes returns the stored bytes without copying (for matching). Valid
// until the next write.
func (a *arena) bytes(i uint32) []byte {
	s := a.start[i]
	return a.buf[s : s+a.n[i] : s+a.n[i]]
}

// Store is the in-memory index.
//
// Base: at build time entries are numbered in depth-first pre-order, so a
// folder's contents are one contiguous id range (in: is a range check).
//
// Overlay: changes apply without a rebuild. New or changed files are
// appended (a fresh id keeps every bitmap exact) and replaced ids go in
// dead, which every query subtracts. Renames and moves happen in place;
// moved entries and their descendants go in moved, where in: walks up the
// parent chain instead of trusting the base range. When the overlay grows
// past a threshold the store is rebuilt from SQLite (NeedsCompaction).
//
// A Store is not safe for concurrent mutation; readers and the writer are
// serialized by the caller.
type Store struct {
	names, folded arena
	parent        []uint32
	subtreeEnd    []uint32
	depth         []uint16
	size          []uint64
	modified      []int64
	created       []int64
	kind          []uint8
	flags         []uint8
	width, height []uint32
	dbID          []int64
	inode         []uint64

	// Children of base entries (CSR over base ids), plus children added or
	// moved in since the build.
	childStart    []uint32
	childIDs      []uint32
	extraChildren map[uint32][]uint32
	// baseLen: ids below this were laid out by the last build.
	baseLen uint32
	dead    *roaring.Bitmap
	moved   *roaring.Bitmap
	pending *roaring.Bitmap

	all      *roaring.Bitmap
	kindBM   []*roaring.Bitmap
	dirBM    *roaring.Bitmap
	hiddenBM *roaring.Bitmap
	datalBM  *roaring.Bitmap
	rawBM    *roaring.Bitmap
	// resBM by res: bucket; orientBM portrait, landscape, square; dimsBM:
	// entries with known dimensions.
	resBM    [6]*roaring.Bitmap
	orientBM [3]*roaring.Bitmap
	dimsBM   *roaring.Bitmap
	extBM    map[string]*roaring.Bitmap
	tagBM    map[string]*roaring.Bitmap
	trigrams map[uint32]*roaring.Bitmap

	// Base ids in ascending order of each column (§2.1.1f); appended ids
	// are merged in at query time.
	bySize, byModified, byCreated []uint32
	roots                         []uint32
	media                         *MediaIndex
	// volume is the folded drive name this index covers ("" = this Mac).
	volume string
}

func newStore() *Store {
	s := &Store{
		all: roaring.New(), dirBM: roaring.New(), hiddenBM: roaring.New(), datalBM: roaring.New(),
		rawBM: roaring.New(), pending: roaring.New(), dimsBM: roaring.New(), dead: roaring.New(), moved: roaring.New(),
		extBM: map[string]*roaring.Bitmap{}, tagBM: map[string]*roaring.Bitmap{}, trigrams: map[uint32]*roaring.Bitmap{},
		extraChildren: map[uint32][]uint32{}, media: newMediaIndex(),
	}
	for range media.Kinds {
		s.kindBM = append(s.kindBM, roaring.New())
	}
	for i := range s.resBM {
		s.resBM[i] = roaring.New()
	}
	for i := range s.orientBM {
		s.orientBM[i] = roaring.New()
	}
	return s
}

// Len is the number of ids (live and dead).
func (s *Store) Len() int { return len(s.parent) }

// SetVolumeName names the drive this index covers (volume: filter).
func (s *Store) SetVolumeName(name string) { s.volume = fold.Fold(name) }

// Build lays out a store from raw entries (any order). Entries unreachable
// from a root are dropped.
func Build(raw []RawEntry) *Store {
	n := len(raw)
	childCount := make([]uint32, n+1)
	for _, e := range raw {
		if e.Parent >= 0 {
			childCount[e.Parent]++
		}
	}
	cstart := make([]uint32, n+1)
	for i := 0; i < n; i++ {
		cstart[i+1] = cstart[i] + childCount[i]
	}
	fill := append([]uint32(nil), cstart...)
	children := make([]uint32, cstart[n])
	for i, e := range raw {
		if e.Parent >= 0 {
			children[fill[e.Parent]] = uint32(i)
			fill[e.Parent]++
		}
	}
	order := make([]uint32, 0, n) // new id → input index
	var stack []uint32
	for i := n - 1; i >= 0; i-- {
		if raw[i].Parent < 0 {
			stack = append(stack, uint32(i))
		}
	}
	for len(stack) > 0 {
		old := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		order = append(order, old)
		kids := children[cstart[old]:cstart[old+1]]
		for j := len(kids) - 1; j >= 0; j-- {
			stack = append(stack, kids[j])
		}
	}
	newID := make([]uint32, n)
	for i := range newID {
		newID[i] = None
	}
	for nw, old := range order {
		newID[old] = uint32(nw)
	}
	m := len(order)

	folded := make([]string, m)
	parallel(m, func(i int) { folded[i] = fold.Fold(raw[order[i]].Name) })

	s := newStore()
	s.baseLen = uint32(m)
	s.childStart = append(make([]uint32, 0, m+1), 0)
	for _, old := range order {
		for _, c := range children[cstart[old]:cstart[old+1]] {
			if newID[c] != None {
				s.childIDs = append(s.childIDs, newID[c])
			}
		}
		s.childStart = append(s.childStart, uint32(len(s.childIDs)))
	}
	sub := make([]uint32, m)
	for i := m - 1; i >= 0; i-- {
		sub[i]++
		if p := raw[order[i]].Parent; p >= 0 && newID[p] != None {
			sub[newID[p]] += sub[i]
		}
	}
	for nw, old := range order {
		e := &raw[old]
		parent := None
		if e.Parent >= 0 {
			parent = newID[e.Parent]
		}
		s.pushEntry(parent, e, folded[nw])
		s.subtreeEnd[nw] = uint32(nw) + sub[nw]
	}
	s.all.AddRange(0, uint64(m))
	for i := 0; i < m; i++ {
		if s.parent[i] == None {
			s.roots = append(s.roots, uint32(i))
		}
	}
	all := make([]uint32, m)
	for i := range all {
		all[i] = uint32(i)
	}
	s.pairSiblings(all)
	s.buildTrigrams()
	s.buildOrders()
	return s
}

// pushEntry appends one entry's columns and bitmaps (ids ascend).
func (s *Store) pushEntry(parent uint32, e *RawEntry, folded string) uint32 {
	id := uint32(len(s.parent))
	w, h := e.Width, e.Height
	if e.Media != nil && w == 0 {
		w, h = e.Media.Width, e.Media.Height
	}
	s.names.push(e.Name)
	s.folded.push(folded)
	s.parent = append(s.parent, parent)
	s.subtreeEnd = append(s.subtreeEnd, id+1)
	var d uint16
	if parent != None && s.depth[parent] < ^uint16(0) {
		d = s.depth[parent] + 1
	}
	s.depth = append(s.depth, d)
	s.size = append(s.size, e.Size)
	s.modified = append(s.modified, e.Modified)
	s.created = append(s.created, e.Created)
	s.width = append(s.width, w)
	s.height = append(s.height, h)
	s.dbID = append(s.dbID, e.DBID)
	s.inode = append(s.inode, e.Inode)
	isRoot := parent == None
	ext := ""
	if !isRoot {
		ext = media.Extension(folded)
	}
	k := media.Classify(ext, e.IsDir, e.Size)
	s.kind = append(s.kind, uint8(k))
	s.kindBM[k].Add(id)
	hidden := !isRoot && (strings.HasPrefix(e.Name, ".") || s.flags[parent]&FlagHidden != 0)
	var flags uint8
	if e.IsDir {
		flags |= FlagDir
		s.dirBM.Add(id)
	}
	if hidden {
		flags |= FlagHidden
		s.hiddenBM.Add(id)
	}
	if e.IsSymlink {
		flags |= FlagSymlink
	}
	if e.Dataless {
		flags |= FlagDataless
		s.datalBM.Add(id)
	}
	s.flags = append(s.flags, flags)
	if e.Pending {
		s.pending.Add(id)
	}
	if !e.IsDir && ext != "" {
		bm := s.extBM[ext]
		if bm == nil {
			bm = roaring.New()
			s.extBM[ext] = bm
		}
		bm.Add(id)
		if media.IsRaw(ext) {
			s.rawBM.Add(id)
		}
	}
	for _, t := range e.Tags {
		t = fold.Fold(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		bm := s.tagBM[t]
		if bm == nil {
			bm = roaring.New()
			s.tagBM[t] = bm
		}
		bm.Add(id)
	}
	s.media.grow(id)
	if e.Media != nil {
		s.media.add(id, e.Media)
	}
	if b, ok := media.ResBucket(w, h); ok {
		s.resBM[b].Add(id)
		s.dimsBM.Add(id)
		switch {
		case w < h:
			s.orientBM[0].Add(id)
		case w > h:
			s.orientBM[1].Add(id)
		default:
			s.orientBM[2].Add(id)
		}
	}
	return id
}

// ---- live updates ---------------------------------------------------

// Append adds a new entry under parent (a live folder) and returns its id.
func (s *Store) Append(parent uint32, e RawEntry) uint32 {
	id := s.pushEntry(parent, &e, fold.Fold(e.Name))
	s.all.Add(id)
	s.extraChildren[parent] = append(s.extraChildren[parent], id)
	s.addTrigrams(id)
	return id
}

// Kill removes one entry (only this id; see Descendants).
func (s *Store) Kill(id uint32) {
	s.dead.Add(id)
	s.pending.Remove(id)
}

// RenameMove renames and/or moves id in place. The caller guarantees the
// extension is unchanged for files (otherwise it replaces the entry), so
// kind and extension bitmaps stay valid.
func (s *Store) RenameMove(id, newParent uint32, newName string) {
	if s.Name(id) != newName {
		s.names.set(id, newName)
		s.folded.set(id, fold.Fold(newName))
		// Old trigrams stay: the prefilter tolerates false positives.
		s.addTrigrams(id)
	}
	var subtree []uint32
	movedNow := false
	if s.parent[id] != newParent {
		s.parent[id] = newParent
		d := s.depth[newParent]
		if d < ^uint16(0) {
			d++
		}
		s.depth[id] = d
		s.extraChildren[newParent] = append(s.extraChildren[newParent], id)
		subtree = s.Descendants(id)
		s.moved.Add(id)
		s.moved.AddMany(subtree)
		movedNow = true
		// Depths of descendants follow (parents come first).
		for _, x := range subtree {
			if p := s.parent[x]; s.depth[p] < ^uint16(0) {
				s.depth[x] = s.depth[p] + 1
			}
		}
	}
	hidden := strings.HasPrefix(newName, ".") || s.flags[newParent]&FlagHidden != 0
	if hidden != (s.flags[id]&FlagHidden != 0) {
		if !movedNow {
			subtree = s.Descendants(id)
		}
		s.setHidden(id, hidden)
		for _, x := range subtree {
			s.setHidden(x, strings.HasPrefix(s.Name(x), ".") || s.flags[s.parent[x]]&FlagHidden != 0)
		}
	}
}

func (s *Store) setHidden(id uint32, h bool) {
	if h {
		s.flags[id] |= FlagHidden
		s.hiddenBM.Add(id)
	} else {
		s.flags[id] &^= FlagHidden
		s.hiddenBM.Remove(id)
	}
}

// SetModified updates a folder's modification time in place.
func (s *Store) SetModified(id uint32, modified int64) { s.modified[id] = modified }

func (s *Store) addTrigrams(id uint32) {
	b := s.folded.bytes(id)
	for i := 0; i+3 <= len(b); i++ {
		t := trigram(b[i : i+3])
		bm := s.trigrams[t]
		if bm == nil {
			bm = roaring.New()
			s.trigrams[t] = bm
		}
		bm.Add(id)
	}
}

// Live is every live entry.
func (s *Store) Live() *roaring.Bitmap { return roaring.AndNot(s.all, s.dead) }

// IsAlive: id exists and was not removed.
func (s *Store) IsAlive(id uint32) bool { return int(id) < s.Len() && !s.dead.Contains(id) }

// LiveCount is the number of live entries that are not root folders.
func (s *Store) LiveCount() uint64 {
	n := s.all.GetCardinality() - s.dead.GetCardinality()
	for _, r := range s.roots {
		if !s.dead.Contains(r) {
			n--
		}
	}
	return n
}

// Children are the live children of a folder, in id order.
func (s *Store) Children(dir uint32) []uint32 {
	var out []uint32
	if dir < s.baseLen {
		out = append(out, s.childIDs[s.childStart[dir]:s.childStart[dir+1]]...)
	}
	out = append(out, s.extraChildren[dir]...)
	out = slices.DeleteFunc(out, func(c uint32) bool { return !s.IsAlive(c) || s.parent[c] != dir })
	slices.Sort(out)
	return slices.Compact(out)
}

// Descendants are the live entries inside dir (not dir), parents first.
func (s *Store) Descendants(dir uint32) []uint32 {
	var out []uint32
	queue := []uint32{dir}
	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		for _, c := range s.Children(d) {
			out = append(out, c)
			if s.IsDir(c) {
				queue = append(queue, c)
			}
		}
	}
	return out
}

// FindChild finds a live child by name (then by folded name, for
// case-insensitive volumes).
func (s *Store) FindChild(dir uint32, name string) (uint32, bool) {
	kids := s.Children(dir)
	for _, c := range kids {
		if s.Name(c) == name {
			return c, true
		}
	}
	f := fold.Fold(name)
	for _, c := range kids {
		if s.FoldedName(c) == f {
			return c, true
		}
	}
	return 0, false
}

// Resolve finds the live entry at an absolute path.
func (s *Store) Resolve(path string) (uint32, bool) {
	path = filepath.Clean(path)
	sep := string(os.PathSeparator)
	for _, r := range s.roots {
		if !s.IsAlive(r) {
			continue
		}
		root := s.names.get(r)
		rest, ok := strings.CutPrefix(path, root)
		if !ok || (rest != "" && !strings.HasPrefix(rest, sep) && root != sep) {
			continue
		}
		cur, found := r, true
		for _, part := range strings.Split(strings.Trim(rest, sep), sep) {
			if part == "" {
				continue
			}
			next, ok := s.FindChild(cur, part)
			if !ok {
				found = false
				break
			}
			cur = next
		}
		if found {
			return cur, true
		}
	}
	return 0, false
}

// Within is every live entry inside dir (not dir itself).
func (s *Store) Within(dir uint32) *roaring.Bitmap {
	out := roaring.New()
	if dir < s.baseLen {
		out.AddRange(uint64(dir)+1, uint64(s.subtreeEnd[dir]))
	}
	// Entries whose ancestry changed since the build (moved or added) are
	// checked by walking up.
	unstable := s.moved.Clone()
	if int(s.baseLen) < s.Len() {
		unstable.AddRange(uint64(s.baseLen), uint64(s.Len()))
	}
	out.AndNot(unstable)
	it := unstable.Iterator()
	for it.HasNext() {
		id := it.Next()
		if s.dead.Contains(id) {
			continue
		}
		for cur := s.parent[id]; cur != None; cur = s.parent[cur] {
			if cur == dir {
				out.Add(id)
				break
			}
		}
	}
	out.AndNot(s.dead)
	return out
}

// OverlaySize counts changes since the last build.
func (s *Store) OverlaySize() uint64 {
	return s.dead.GetCardinality() + s.moved.GetCardinality() + uint64(s.Len()) - uint64(s.baseLen)
}

// NeedsCompaction: the overlay is big enough that a rebuild is worth it.
func (s *Store) NeedsCompaction() bool {
	return s.OverlaySize() > max(50_000, uint64(s.Len())/10)
}

// RepairSiblings recomputes neighbour-dependent flags for dir's children.
func (s *Store) RepairSiblings(dir uint32) {
	kids := s.Children(dir)
	for _, k := range kids {
		s.media.clearFlag(flagLive, k)
		s.media.clearFlag(flagSidecar, k)
		s.media.clearFlag(flagScreenshot, k)
	}
	s.pairSiblings(kids)
}

var subtitleExts = map[string]bool{"srt": true, "ass": true, "ssa": true, "vtt": true, "sub": true, "sup": true}

// pairSiblings sets flags that depend on neighbouring files (§3.4, §3.7):
// Live Photos (HEIC/JPEG + MOV with the same stem), videos with sidecar
// subtitles (movie.srt, movie.en.srt), and screenshots by name.
func (s *Store) pairSiblings(ids []uint32) {
	type key struct {
		parent uint32
		stem   string
	}
	type item struct {
		id  uint32
		ext string
	}
	byStem := map[key][]item{}
	var subs []item // ext holds the stem
	var screenshots []uint32
	for _, id := range ids {
		if s.IsDir(id) || s.IsRoot(id) || s.dead.Contains(id) {
			continue
		}
		name := s.FoldedName(id)
		if strings.HasPrefix(name, "screenshot") || strings.HasPrefix(name, "screen shot") || strings.HasPrefix(name, "لقطه شاشه") {
			screenshots = append(screenshots, id)
		}
		ext := media.Extension(name)
		stem := strings.TrimRight(name[:len(name)-len(ext)], ".")
		k := media.Kind(s.kind[id])
		switch {
		case subtitleExts[ext]:
			subs = append(subs, item{id, stem})
		case k == media.Video || k == media.Image:
			kk := key{s.parent[id], stem}
			byStem[kk] = append(byStem[kk], item{id, ext})
		}
	}
	for _, group := range byStem {
		still, motion := None, None
		for _, it := range group {
			switch it.ext {
			case "heic", "heif", "jpg", "jpeg":
				if still == None {
					still = it.id
				}
			case "mov":
				if motion == None {
					motion = it.id
				}
			}
		}
		if still != None && motion != None {
			s.media.setFlag(flagLive, still)
			s.media.setFlag(flagLive, motion)
		}
	}
	for _, sub := range subs {
		parent := s.parent[sub.id]
		// movie.en.forced.srt → movie.en.forced, movie.en, movie
		for st := sub.ext; ; {
			if group, ok := byStem[key{parent, st}]; ok {
				for _, it := range group {
					if media.Kind(s.kind[it.id]) == media.Video {
						s.media.setFlag(flagSidecar, it.id)
					}
				}
				break
			}
			i := strings.LastIndexByte(st, '.')
			if i < 0 {
				break
			}
			st = st[:i]
		}
	}
	for _, id := range screenshots {
		s.media.setFlag(flagScreenshot, id)
	}
}

func trigram(b []byte) uint32 { return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2]) }

// buildTrigrams indexes every 3-byte window of every folded name.
func (s *Store) buildTrigrams() {
	n := s.Len()
	workers := runtime.GOMAXPROCS(0)
	parts := make([]map[uint32][]uint32, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			m := map[uint32][]uint32{}
			seen := map[uint32]bool{}
			for id := w * n / workers; id < (w+1)*n/workers; id++ {
				b := s.folded.bytes(uint32(id))
				clear(seen)
				for i := 0; i+3 <= len(b); i++ {
					t := trigram(b[i : i+3])
					if !seen[t] {
						seen[t] = true
						m[t] = append(m[t], uint32(id))
					}
				}
			}
			parts[w] = m
		}(w)
	}
	wg.Wait()
	// Parts cover ascending id ranges, so concatenating keeps ids sorted.
	for _, m := range parts {
		for t, ids := range m {
			bm := s.trigrams[t]
			if bm == nil {
				bm = roaring.New()
				s.trigrams[t] = bm
			}
			bm.AddMany(ids)
		}
	}
}

// buildOrders pre-sorts ids by size and dates.
func (s *Store) buildOrders() {
	n := s.Len()
	mk := func(key func(uint32) int64) []uint32 {
		o := make([]uint32, n)
		for i := range o {
			o[i] = uint32(i)
		}
		slices.SortStableFunc(o, func(a, b uint32) int {
			ka, kb := key(a), key(b)
			switch {
			case ka < kb:
				return -1
			case ka > kb:
				return 1
			}
			return 0
		})
		return o
	}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		// Sizes above 2^63 do not exist on real disks.
		s.bySize = mk(func(i uint32) int64 { return int64(min(s.size[i], 1<<63-1)) })
	}()
	go func() { defer wg.Done(); s.byModified = mk(func(i uint32) int64 { return s.modified[i] }) }()
	go func() { defer wg.Done(); s.byCreated = mk(func(i uint32) int64 { return s.created[i] }) }()
	wg.Wait()
}

// TrigramCandidates are the entries whose name contains every trigram of
// lit; nil when lit is too short to filter on.
func (s *Store) TrigramCandidates(lit string) *roaring.Bitmap {
	b := []byte(lit)
	if len(b) < 3 {
		return nil
	}
	var out *roaring.Bitmap
	for i := 0; i+3 <= len(b); i++ {
		bm := s.trigrams[trigram(b[i:i+3])]
		if bm == nil {
			return roaring.New()
		}
		if out == nil {
			out = bm.Clone()
		} else {
			out.And(bm)
		}
		if out.IsEmpty() {
			return out
		}
	}
	return out
}

// Accessors.

func (s *Store) Name(id uint32) string       { return s.names.get(id) }
func (s *Store) FoldedName(id uint32) string { return s.folded.get(id) }
func (s *Store) IsDir(id uint32) bool        { return s.flags[id]&FlagDir != 0 }
func (s *Store) IsSymlink(id uint32) bool    { return s.flags[id]&FlagSymlink != 0 }
func (s *Store) IsDataless(id uint32) bool   { return s.flags[id]&FlagDataless != 0 }
func (s *Store) IsHidden(id uint32) bool     { return s.flags[id]&FlagHidden != 0 }
func (s *Store) IsRoot(id uint32) bool       { return s.parent[id] == None }
func (s *Store) Parent(id uint32) uint32     { return s.parent[id] }
func (s *Store) Size(id uint32) uint64       { return s.size[id] }
func (s *Store) Modified(id uint32) int64    { return s.modified[id] }
func (s *Store) Created(id uint32) int64     { return s.created[id] }
func (s *Store) Kind(id uint32) media.Kind   { return media.Kind(s.kind[id]) }
func (s *Store) Depth(id uint32) uint16      { return s.depth[id] }
func (s *Store) DBID(id uint32) int64        { return s.dbID[id] }
func (s *Store) Inode(id uint32) uint64      { return s.inode[id] }
func (s *Store) IsPending(id uint32) bool    { return s.pending.Contains(id) }
func (s *Store) HasMedia(id uint32) bool     { return s.media.has(id) }

// SetPending marks or clears a file still being written.
func (s *Store) SetPending(id uint32, p bool) {
	if p {
		s.pending.Add(id)
	} else {
		s.pending.Remove(id)
	}
}

// Pending are the live entries waiting for a stable probe.
func (s *Store) Pending() *roaring.Bitmap { return roaring.AndNot(s.pending, s.dead) }

// Dimensions, if known.
func (s *Store) Dimensions(id uint32) (w, h uint32, ok bool) {
	return s.width[id], s.height[id], s.width[id] > 0 && s.height[id] > 0
}

// Path is the absolute path of id.
func (s *Store) Path(id uint32) string {
	var parts []string
	for cur := id; cur != None; cur = s.parent[cur] {
		parts = append(parts, s.names.get(cur))
	}
	slices.Reverse(parts)
	return filepath.Join(parts...)
}

// FoldedPath is the folded absolute path (for patterns containing /).
func (s *Store) FoldedPath(id uint32) string { return fold.Fold(s.Path(id)) }

// RootIDs are the live root folders' ids.
func (s *Store) RootIDs() []uint32 {
	var out []uint32
	for _, r := range s.roots {
		if s.IsAlive(r) {
			out = append(out, r)
		}
	}
	return out
}

// Roots are the live indexed folders' paths.
func (s *Store) Roots() []string {
	out := []string{}
	for _, r := range s.RootIDs() {
		out = append(out, s.names.get(r))
	}
	return out
}

// TextValues are every indexed value of a text filter (for "did you mean"
// coaching, §8.3): extensions, tags, or a media facet in any scope.
func (s *Store) TextValues(id query.FilterID) []string {
	var v []string
	switch id {
	case query.FExt:
		for k := range s.extBM {
			v = append(v, k)
		}
	case query.FTag:
		for k := range s.tagBM {
			v = append(v, k)
		}
	default:
		for fk, m := range s.media.facets {
			if fk.id == id {
				for val := range m {
					v = append(v, val)
				}
			}
		}
	}
	slices.Sort(v)
	return slices.Compact(v)
}

// MemoryBytes estimates the heap held by the store.
func (s *Store) MemoryBytes() uint64 {
	n := uint64(s.Len())
	b := uint64(len(s.names.buf)+len(s.folded.buf)) + n*(4*2*2)
	b += n * (4 + 4 + 2 + 8 + 8 + 8 + 1 + 1 + 4 + 4 + 8 + 8)
	b += uint64(len(s.childIDs))*4 + n*4*4
	bms := []*roaring.Bitmap{s.all, s.dirBM, s.hiddenBM, s.datalBM, s.rawBM, s.pending, s.dimsBM, s.dead, s.moved}
	bms = append(bms, s.kindBM...)
	bms = append(bms, s.resBM[:]...)
	bms = append(bms, s.orientBM[:]...)
	for _, m := range []map[string]*roaring.Bitmap{s.extBM, s.tagBM} {
		for _, bm := range m {
			bms = append(bms, bm)
		}
	}
	for _, bm := range s.trigrams {
		bms = append(bms, bm)
	}
	for _, bm := range bms {
		b += bm.GetSizeInBytes()
	}
	return b + s.media.memoryBytes()
}

// parallel runs f(0..n-1) across the cores.
func parallel(n int, f func(int)) {
	workers := runtime.GOMAXPROCS(0)
	if n < 1024 || workers == 1 {
		for i := 0; i < n; i++ {
			f(i)
		}
		return
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w * n / workers; i < (w+1)*n/workers; i++ {
				f(i)
			}
		}(w)
	}
	wg.Wait()
}

func numCPU() int { return runtime.GOMAXPROCS(0) }
