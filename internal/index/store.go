// Package index is the durable store (SQLite), the in-memory store that
// answers queries (spec §2.1.1a–f), and query execution and ranking.
package index

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/jamalxcode/laserdot-go/internal/fold"
	"github.com/jamalxcode/laserdot-go/internal/media"
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

// RawEntry is an input row for Build.
type RawEntry struct {
	// Parent is the index of the parent in the input slice; -1 for a root
	// folder, whose Name is its absolute path.
	Parent    int
	Name      string
	IsDir     bool
	IsSymlink bool
	Dataless  bool
	Size      uint64
	Modified  int64
	Created   int64
	// Width and Height: display dimensions, 0 when unknown (media, G2).
	Width, Height uint32
	DBID          int64
	Tags          []string
	// Pending: a media file still being written (§2.6.3).
	Pending bool
	Inode   uint64
}

// arena stores strings as offsets into one buffer.
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

func (a *arena) get(i uint32) string {
	s := a.start[i]
	return string(a.buf[s : s+a.n[i]])
}

// bytes returns the stored bytes without copying (for matching).
func (a *arena) bytes(i uint32) []byte {
	s := a.start[i]
	return a.buf[s : s+a.n[i]]
}

// Store is the in-memory index. Entries are numbered in depth-first
// pre-order, so a folder's contents are one contiguous id range and in: is
// a range check.
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
	childStart    []uint32
	childIDs      []uint32

	all      *roaring.Bitmap
	kindBM   []*roaring.Bitmap
	dirBM    *roaring.Bitmap
	hiddenBM *roaring.Bitmap
	datalBM  *roaring.Bitmap
	rawBM    *roaring.Bitmap
	pending  *roaring.Bitmap
	// resBM by res: bucket; orientBM portrait, landscape, square; dimsBM:
	// entries with known dimensions.
	resBM    [6]*roaring.Bitmap
	orientBM [3]*roaring.Bitmap
	dimsBM   *roaring.Bitmap
	extBM    map[string]*roaring.Bitmap
	tagBM    map[string]*roaring.Bitmap
	trigrams map[uint32]*roaring.Bitmap

	bySize, byModified, byCreated []uint32
	roots                         []uint32
	// volume is the folded drive name this index covers ("" = this Mac).
	volume string
}

// Len is the number of entries.
func (s *Store) Len() int { return len(s.parent) }

// Build lays out a store from raw entries (any order, parents before or
// after children).
func Build(raw []RawEntry) *Store {
	n := len(raw)
	// Children (over input indices), subtree sizes, then pre-order ids.
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
	// Entries unreachable from a root (an orphan row) are dropped.
	m := len(order)

	// Fold names in parallel (the costly part for non-ASCII names).
	folded := make([]string, m)
	parallel(m, func(i int) { folded[i] = fold.Fold(raw[order[i]].Name) })

	s := &Store{
		all: roaring.New(), dirBM: roaring.New(), hiddenBM: roaring.New(), datalBM: roaring.New(),
		rawBM: roaring.New(), pending: roaring.New(), dimsBM: roaring.New(),
		extBM: map[string]*roaring.Bitmap{}, tagBM: map[string]*roaring.Bitmap{}, trigrams: map[uint32]*roaring.Bitmap{},
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
	s.childStart = append(s.childStart, 0)
	for _, old := range order {
		for _, c := range children[cstart[old]:cstart[old+1]] {
			if newID[c] != None {
				s.childIDs = append(s.childIDs, newID[c])
			}
		}
		s.childStart = append(s.childStart, uint32(len(s.childIDs)))
	}
	// Subtree ends from the pre-order: a node's subtree is contiguous.
	sub := make([]uint32, m)
	for i := m - 1; i >= 0; i-- {
		sub[i]++
		if p := raw[order[i]].Parent; p >= 0 && newID[p] != None {
			sub[newID[p]] += sub[i]
		}
	}
	for nw, old := range order {
		e := raw[old]
		parent := None
		if e.Parent >= 0 {
			parent = newID[e.Parent]
		}
		s.push(uint32(nw), parent, &e, folded[nw])
		s.subtreeEnd = append(s.subtreeEnd, uint32(nw)+sub[nw])
	}
	s.all.AddRange(0, uint64(m))
	for i := 0; i < m; i++ {
		if s.parent[i] == None {
			s.roots = append(s.roots, uint32(i))
		}
	}
	s.buildTrigrams()
	s.buildOrders()
	return s
}

func (s *Store) push(id, parent uint32, e *RawEntry, folded string) {
	s.names.push(e.Name)
	s.folded.push(folded)
	s.parent = append(s.parent, parent)
	var d uint16
	if parent != None {
		d = s.depth[parent] + 1
	}
	s.depth = append(s.depth, d)
	s.size = append(s.size, e.Size)
	s.modified = append(s.modified, e.Modified)
	s.created = append(s.created, e.Created)
	s.width = append(s.width, e.Width)
	s.height = append(s.height, e.Height)
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
	if b, ok := media.ResBucket(e.Width, e.Height); ok {
		s.resBM[b].Add(id)
		s.dimsBM.Add(id)
		switch {
		case e.Width < e.Height:
			s.orientBM[0].Add(id)
		case e.Width > e.Height:
			s.orientBM[1].Add(id)
		default:
			s.orientBM[2].Add(id)
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
}

func trigram(b []byte) uint32 { return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2]) }

// buildTrigrams indexes every 3-byte window of every folded name
// (§2.1.1b).
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
			for id := w * n / workers; id < (w+1)*n/workers; id++ {
				b := s.folded.bytes(uint32(id))
				seen := map[uint32]bool{}
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

// buildOrders pre-sorts ids by size and dates (§2.1.1f).
func (s *Store) buildOrders() {
	n := s.Len()
	mk := func(less func(a, b uint32) bool) []uint32 {
		o := make([]uint32, n)
		for i := range o {
			o[i] = uint32(i)
		}
		sort.SliceStable(o, func(i, j int) bool { return less(o[i], o[j]) })
		return o
	}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); s.bySize = mk(func(a, b uint32) bool { return s.size[a] < s.size[b] }) }()
	go func() {
		defer wg.Done()
		s.byModified = mk(func(a, b uint32) bool { return s.modified[a] < s.modified[b] })
	}()
	go func() {
		defer wg.Done()
		s.byCreated = mk(func(a, b uint32) bool { return s.created[a] < s.created[b] })
	}()
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
func (s *Store) IsRoot(id uint32) bool       { return s.parent[id] == None }
func (s *Store) Size(id uint32) uint64       { return s.size[id] }
func (s *Store) Modified(id uint32) int64    { return s.modified[id] }
func (s *Store) Created(id uint32) int64     { return s.created[id] }
func (s *Store) Kind(id uint32) media.Kind   { return media.Kind(s.kind[id]) }
func (s *Store) Depth(id uint32) uint16      { return s.depth[id] }
func (s *Store) DBID(id uint32) int64        { return s.dbID[id] }
func (s *Store) Inode(id uint32) uint64      { return s.inode[id] }
func (s *Store) IsPending(id uint32) bool    { return s.pending.Contains(id) }

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
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return filepath.Join(parts...)
}

// FoldedPath is the folded absolute path (for patterns containing /).
func (s *Store) FoldedPath(id uint32) string { return fold.Fold(s.Path(id)) }

// Roots are the indexed folders' paths.
func (s *Store) Roots() []string {
	out := make([]string, len(s.roots))
	for i, r := range s.roots {
		out[i] = s.names.get(r)
	}
	return out
}

// Children of a folder.
func (s *Store) Children(dir uint32) []uint32 {
	return s.childIDs[s.childStart[dir]:s.childStart[dir+1]]
}

// Within is every entry inside dir (not dir itself).
func (s *Store) Within(dir uint32) *roaring.Bitmap {
	bm := roaring.New()
	bm.AddRange(uint64(dir)+1, uint64(s.subtreeEnd[dir]))
	return bm
}

// Resolve finds the entry at an absolute path.
func (s *Store) Resolve(path string) (uint32, bool) {
	path = filepath.Clean(path)
	for _, r := range s.roots {
		root := s.names.get(r)
		rest, ok := strings.CutPrefix(path, root)
		if !ok || (rest != "" && !strings.HasPrefix(rest, string(os.PathSeparator)) && root != "/") {
			continue
		}
		cur := r
		found := true
		for _, part := range strings.Split(strings.Trim(rest, string(os.PathSeparator)), string(os.PathSeparator)) {
			if part == "" {
				continue
			}
			next, ok := s.findChild(cur, part)
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

func (s *Store) findChild(dir uint32, name string) (uint32, bool) {
	for _, c := range s.Children(dir) {
		if s.names.get(c) == name {
			return c, true
		}
	}
	return 0, false
}

// Live is every entry (G1 has no deletions in memory).
func (s *Store) Live() *roaring.Bitmap { return s.all.Clone() }

// LiveCount is the number of entries that are not root folders.
func (s *Store) LiveCount() uint64 { return s.all.GetCardinality() - uint64(len(s.roots)) }

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

// MemoryBytes estimates the heap held by the store.
func (s *Store) MemoryBytes() uint64 {
	n := uint64(s.Len())
	b := uint64(len(s.names.buf)+len(s.folded.buf)) + n*(4*2*2)  // arenas + offsets
	b += n * (4 + 4 + 2 + 8 + 8 + 8 + 1 + 1 + 4 + 4 + 8 + 8 + 4) // columns
	b += uint64(len(s.childIDs))*4 + n*4*3                       // children + sort orders
	bms := []*roaring.Bitmap{s.all, s.dirBM, s.hiddenBM, s.datalBM, s.rawBM, s.pending, s.dimsBM}
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
	return b
}
