package index

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"runtime"
	"slices"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
	"golang.org/x/sys/unix"

	"github.com/jamalxcode/laserdot-go/internal/media"
	"github.com/jamalxcode/laserdot-go/internal/query"
)

// The snapshot (spec §2.1.1g): the in-memory store written to one file and
// mapped at launch, so a cold start reads no SQLite and the columns are
// file-backed page cache rather than dirty memory (§12.7).
//
// Format: "LDSNAP\x00\x00" | version u32 | endian u32 | token u64 | sections
// | crc32c u32 of everything before it. Every section is a u64 length and
// its bytes, padded to 8, so typed columns are aligned in the mapping. The
// mapping is private and writable: live updates write to copy-on-write
// pages and never touch the file.
//
// A snapshot is used only when its token matches the database's; every
// write to the database clears that token. Any change to the layout below
// must bump snapshotVersion.

const snapshotVersion = 1

var snapMagic = []byte("LDSNAP\x00\x00")

const snapEndian uint32 = 0x01020304

// SnapshotPath is the snapshot next to an index database.
func SnapshotPath(db string) string { return db + ".snapshot" }

type snapWriter struct {
	w   *bufio.Writer
	crc uint32
	n   int64
	err error
}

func (w *snapWriter) raw(b []byte) {
	if w.err != nil {
		return
	}
	_, w.err = w.w.Write(b)
	w.crc = crc32.Update(w.crc, crcTable, b)
	w.n += int64(len(b))
}

func (w *snapWriter) u64(v uint64) { w.raw(binary.LittleEndian.AppendUint64(nil, v)) }

func (w *snapWriter) section(b []byte) {
	w.u64(uint64(len(b)))
	w.raw(b)
	if pad := (8 - len(b)%8) % 8; pad > 0 {
		w.raw(make([]byte, pad))
	}
}

func asBytes[T any](s []T) []byte {
	if len(s) == 0 {
		return nil
	}
	var z T
	return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), len(s)*int(unsafe.Sizeof(z)))
}

func putCol[T any](w *snapWriter, s []T) { w.section(asBytes(s)) }

func (w *snapWriter) bitmap(bm *roaring.Bitmap) {
	// The store is being searched while it is written (under a read lock):
	// compact a copy, never the live bitmap.
	c := bm.Clone()
	c.RunOptimize()
	b, err := c.ToBytes()
	if err != nil && w.err == nil {
		w.err = err
	}
	w.section(b)
}

func (w *snapWriter) str(s string) { w.section([]byte(s)) }

func (w *snapWriter) strMap(m map[string]*roaring.Bitmap) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	w.u64(uint64(len(keys)))
	for _, k := range keys {
		w.str(k)
		w.bitmap(m[k])
	}
}

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// WriteSnapshot writes s to path atomically, tagged with token.
func WriteSnapshot(s *Store, path string, token uint64) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w := &snapWriter{w: bufio.NewWriterSize(f, 1<<20)}
	w.raw(snapMagic)
	w.raw(binary.LittleEndian.AppendUint32(nil, snapshotVersion))
	w.raw(binary.LittleEndian.AppendUint32(nil, snapEndian))
	w.u64(token)
	writeStore(w, s)
	if w.err == nil {
		_, w.err = w.w.Write(binary.LittleEndian.AppendUint32(nil, w.crc))
	}
	if w.err == nil {
		w.err = w.w.Flush()
	}
	if w.err == nil {
		w.err = f.Sync()
	}
	f.Close()
	if w.err != nil {
		os.Remove(tmp)
		return w.err
	}
	return os.Rename(tmp, path)
}

func writeArena(w *snapWriter, a *arena) {
	putCol(w, a.buf)
	putCol(w, a.start)
	putCol(w, a.n)
}

func writeStore(w *snapWriter, s *Store) {
	// Exhaustive by construction: every field of Store appears here, in
	// order. A new field must be added (and snapshotVersion bumped).
	writeArena(w, &s.names)
	writeArena(w, &s.folded)
	putCol(w, s.parent)
	putCol(w, s.subtreeEnd)
	putCol(w, s.depth)
	putCol(w, s.size)
	putCol(w, s.modified)
	putCol(w, s.created)
	putCol(w, s.kind)
	putCol(w, s.flags)
	putCol(w, s.width)
	putCol(w, s.height)
	putCol(w, s.dbID)
	putCol(w, s.inode)
	putCol(w, s.childStart)
	putCol(w, s.childIDs)
	keys := make([]uint32, 0, len(s.extraChildren))
	for k := range s.extraChildren {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var flat []uint32
	for _, k := range keys {
		flat = append(flat, k, uint32(len(s.extraChildren[k])))
		flat = append(flat, s.extraChildren[k]...)
	}
	putCol(w, flat)
	w.u64(uint64(s.baseLen))
	for _, bm := range []*roaring.Bitmap{s.dead, s.moved, s.pending, s.all, s.dirBM, s.hiddenBM, s.datalBM, s.rawBM, s.dimsBM} {
		w.bitmap(bm)
	}
	w.u64(uint64(len(s.kindBM)))
	for _, bm := range s.kindBM {
		w.bitmap(bm)
	}
	for _, bm := range s.resBM {
		w.bitmap(bm)
	}
	for _, bm := range s.orientBM {
		w.bitmap(bm)
	}
	w.strMap(s.extBM)
	w.strMap(s.tagBM)
	tkeys := make([]uint32, 0, len(s.trigrams))
	for k := range s.trigrams {
		tkeys = append(tkeys, k)
	}
	slices.Sort(tkeys)
	putCol(w, tkeys)
	for _, k := range tkeys {
		w.bitmap(s.trigrams[k])
	}
	putCol(w, s.bySize)
	putCol(w, s.byModified)
	putCol(w, s.byCreated)
	putCol(w, s.roots)
	writeMedia(w, s.media)
	w.str(s.volume)
}

func writeMedia(w *snapWriter, m *MediaIndex) {
	putCol(w, m.row)
	w.bitmap(m.all)
	putCol(w, m.durationMS)
	putCol(w, m.bitrate)
	putCol(w, m.fpsMilli)
	putCol(w, m.bitDepth)
	putCol(w, m.bpp)
	putCol(w, m.sampleRate)
	putCol(w, m.counts)
	putCol(w, m.iso)
	putCol(w, m.aperture)
	putCol(w, m.focal)
	putCol(w, m.taken)
	putCol(w, m.year)
	var blob []byte
	offs := make([]uint64, 0, len(m.tracks)+1)
	for _, t := range m.tracks {
		offs = append(offs, uint64(len(blob)))
		blob = append(blob, (&media.Record{Tracks: t}).Encode()...)
	}
	offs = append(offs, uint64(len(blob)))
	putCol(w, offs)
	w.section(blob)
	fkeys := make([]facetKey, 0, len(m.facets))
	for k := range m.facets {
		fkeys = append(fkeys, k)
	}
	slices.SortFunc(fkeys, func(a, b facetKey) int {
		if a.id != b.id {
			return int(a.id) - int(b.id)
		}
		return int(a.scope) - int(b.scope)
	})
	w.u64(uint64(len(fkeys)))
	for _, k := range fkeys {
		w.u64(uint64(k.id))
		w.u64(uint64(k.scope))
		w.strMap(m.facets[k])
	}
	for _, bm := range m.flags {
		w.bitmap(bm)
	}
}

// ---- loading -----------------------------------------------------------

var errSnapshot = errors.New("damaged snapshot")

type snapReader struct {
	b   []byte
	off int
	err error
}

func (r *snapReader) u64() uint64 {
	if r.err != nil || r.off+8 > len(r.b) {
		r.err = errSnapshot
		return 0
	}
	v := binary.LittleEndian.Uint64(r.b[r.off:])
	r.off += 8
	return v
}

func (r *snapReader) section() []byte {
	n := r.u64()
	if r.err != nil || n > uint64(len(r.b)-r.off) {
		r.err = errSnapshot
		return nil
	}
	s := r.b[r.off : r.off+int(n) : r.off+int(n)]
	r.off += int(n) + (8-int(n)%8)%8
	if r.off > len(r.b) {
		r.err = errSnapshot
	}
	return s
}

func getCol[T any](r *snapReader) []T {
	b := r.section()
	var z T
	size := int(unsafe.Sizeof(z))
	if r.err != nil || len(b)%size != 0 {
		r.err = errSnapshot
		return nil
	}
	if len(b) == 0 {
		return nil
	}
	return unsafe.Slice((*T)(unsafe.Pointer(&b[0])), len(b)/size)
}

func (r *snapReader) bitmap() *roaring.Bitmap {
	b := r.section()
	bm := roaring.New()
	if r.err == nil {
		if _, err := bm.FromBuffer(b); err != nil {
			r.err = errSnapshot
		}
	}
	return bm
}

func (r *snapReader) str() string { return string(r.section()) }

func (r *snapReader) strMap() map[string]*roaring.Bitmap {
	n := r.u64()
	if n > uint64(len(r.b)) {
		r.err = errSnapshot
		return nil
	}
	m := make(map[string]*roaring.Bitmap, n)
	for i := uint64(0); i < n && r.err == nil; i++ {
		k := r.str()
		m[k] = r.bitmap()
	}
	return m
}

// mapping keeps a snapshot mapped while a store uses it.
type mapping struct{ data []byte }

func (m *mapping) release() { unix.Munmap(m.data) }

// LoadSnapshot maps the snapshot at path if it was written with token. It
// returns nil (no error) when the file is missing, stale or of another
// version; an error when it is damaged.
func LoadSnapshot(path string, token uint64) (*Store, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, 24)
	if _, err := io.ReadFull(f, head); err != nil || !bytes.Equal(head[:8], snapMagic) {
		return nil, errSnapshot
	}
	if binary.LittleEndian.Uint32(head[8:]) != snapshotVersion || binary.LittleEndian.Uint32(head[12:]) != snapEndian ||
		binary.LittleEndian.Uint64(head[16:]) != token {
		return nil, nil
	}
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() < 28 || fi.Size() > 1<<40 {
		return nil, errSnapshot
	}
	data, err := unix.Mmap(int(f.Fd()), 0, int(fi.Size()), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE)
	if err != nil {
		return nil, err
	}
	m := &mapping{data}
	body := data[:len(data)-4]
	if crc32.Checksum(body, crcTable) != binary.LittleEndian.Uint32(data[len(data)-4:]) {
		m.release()
		return nil, errSnapshot
	}
	r := &snapReader{b: body, off: 24}
	s, err := readStore(r)
	if err == nil && r.off != len(body) {
		err = errSnapshot
	}
	if err != nil {
		m.release()
		return nil, err
	}
	s.mapped = m
	s.mappedBytes = uint64(len(data))
	runtime.SetFinalizer(s, func(s *Store) { s.mapped.release() })
	return s, nil
}

func readArena(r *snapReader, a *arena) {
	a.buf = getCol[byte](r)
	a.start = getCol[uint32](r)
	a.n = getCol[uint32](r)
}

func readStore(r *snapReader) (*Store, error) {
	s := newStore()
	readArena(r, &s.names)
	readArena(r, &s.folded)
	s.parent = getCol[uint32](r)
	s.subtreeEnd = getCol[uint32](r)
	s.depth = getCol[uint16](r)
	s.size = getCol[uint64](r)
	s.modified = getCol[int64](r)
	s.created = getCol[int64](r)
	s.kind = getCol[uint8](r)
	s.flags = getCol[uint8](r)
	s.width = getCol[uint32](r)
	s.height = getCol[uint32](r)
	s.dbID = getCol[int64](r)
	s.inode = getCol[uint64](r)
	s.childStart = getCol[uint32](r)
	s.childIDs = getCol[uint32](r)
	flat := getCol[uint32](r)
	for i := 0; i+1 < len(flat); {
		k, n := flat[i], int(flat[i+1])
		if i+2+n > len(flat) {
			return nil, errSnapshot
		}
		s.extraChildren[k] = slices.Clone(flat[i+2 : i+2+n])
		i += 2 + n
	}
	s.baseLen = uint32(r.u64())
	for _, p := range []**roaring.Bitmap{&s.dead, &s.moved, &s.pending, &s.all, &s.dirBM, &s.hiddenBM, &s.datalBM, &s.rawBM, &s.dimsBM} {
		*p = r.bitmap()
	}
	if nk := r.u64(); nk != uint64(len(media.Kinds)) {
		return nil, errSnapshot
	}
	for i := range s.kindBM {
		s.kindBM[i] = r.bitmap()
	}
	for i := range s.resBM {
		s.resBM[i] = r.bitmap()
	}
	for i := range s.orientBM {
		s.orientBM[i] = r.bitmap()
	}
	s.extBM = r.strMap()
	s.tagBM = r.strMap()
	tkeys := getCol[uint32](r)
	s.trigrams = make(map[uint32]*roaring.Bitmap, len(tkeys))
	for _, k := range tkeys {
		s.trigrams[k] = r.bitmap()
	}
	s.bySize = getCol[uint32](r)
	s.byModified = getCol[uint32](r)
	s.byCreated = getCol[uint32](r)
	s.roots = getCol[uint32](r)
	var err error
	if s.media, err = readMedia(r); err != nil {
		return nil, err
	}
	s.volume = r.str()
	if r.err != nil {
		return nil, r.err
	}
	return s, validate(s)
}

func readMedia(r *snapReader) (*MediaIndex, error) {
	m := newMediaIndex()
	m.row = getCol[uint32](r)
	m.all = r.bitmap()
	m.durationMS = getCol[uint32](r)
	m.bitrate = getCol[uint32](r)
	m.fpsMilli = getCol[uint32](r)
	m.bitDepth = getCol[uint8](r)
	m.bpp = getCol[float32](r)
	m.sampleRate = getCol[uint32](r)
	m.counts = getCol[[3]uint8](r)
	m.iso = getCol[uint32](r)
	m.aperture = getCol[float32](r)
	m.focal = getCol[float32](r)
	m.taken = getCol[int64](r)
	m.year = getCol[uint32](r)
	offs := getCol[uint64](r)
	blob := r.section()
	if r.err != nil {
		return nil, r.err
	}
	for i := 0; i+1 < len(offs); i++ {
		if offs[i] > offs[i+1] || offs[i+1] > uint64(len(blob)) {
			return nil, errSnapshot
		}
		rec, err := media.Decode(blob[offs[i]:offs[i+1]])
		if err != nil {
			return nil, errSnapshot
		}
		m.tracks = append(m.tracks, rec.Tracks)
	}
	n := r.u64()
	if n > uint64(len(r.b)) {
		return nil, errSnapshot
	}
	for i := uint64(0); i < n && r.err == nil; i++ {
		k := facetKey{query.FilterID(r.u64()), query.Scope(r.u64())}
		m.facets[k] = r.strMap()
	}
	for i := range m.flags {
		m.flags[i] = r.bitmap()
	}
	return m, r.err
}

// validate checks the structure, so a snapshot that passes the checksum
// but was written by a buggy build cannot crash a query.
func validate(s *Store) error {
	n := len(s.parent)
	cols := []int{len(s.subtreeEnd), len(s.depth), len(s.size), len(s.modified), len(s.created), len(s.kind),
		len(s.flags), len(s.width), len(s.height), len(s.dbID), len(s.inode), len(s.names.start), len(s.names.n),
		len(s.folded.start), len(s.folded.n)}
	for _, c := range cols {
		if c != n {
			return fmt.Errorf("%w: column length", errSnapshot)
		}
	}
	if int(s.baseLen) > n || len(s.childStart) != int(s.baseLen)+1 || len(s.bySize) != int(s.baseLen) ||
		len(s.byModified) != int(s.baseLen) || len(s.byCreated) != int(s.baseLen) || len(s.media.row) > n {
		return fmt.Errorf("%w: layout", errSnapshot)
	}
	for _, a := range []*arena{&s.names, &s.folded} {
		for i := range a.start {
			if uint64(a.start[i])+uint64(a.n[i]) > uint64(len(a.buf)) {
				return fmt.Errorf("%w: names", errSnapshot)
			}
		}
	}
	for i, p := range s.parent {
		if (p != None && int(p) >= n) || s.subtreeEnd[i] > uint32(n) || s.kind[i] >= uint8(len(media.Kinds)) {
			return fmt.Errorf("%w: entries", errSnapshot)
		}
	}
	if s.all.GetCardinality() > 0 && s.all.Maximum() >= uint32(n) {
		return fmt.Errorf("%w: bitmaps", errSnapshot)
	}
	rows := uint32(len(s.media.durationMS))
	for _, r := range s.media.row {
		if r != None && r >= rows {
			return fmt.Errorf("%w: media rows", errSnapshot)
		}
	}
	if len(s.media.tracks) != int(rows) || len(s.media.counts) != int(rows) {
		return fmt.Errorf("%w: media columns", errSnapshot)
	}
	for _, c := range s.childIDs {
		if int(c) >= n {
			return fmt.Errorf("%w: children", errSnapshot)
		}
	}
	return nil
}
