package core

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/media"
	"github.com/jamalxcode/laserdot-go/internal/query"
)

// The duplicate finder (spec §4): byte-identical copies (size → quick
// hash → full hash), similar images (DCT pHash within a Hamming distance),
// and re-encoded videos (similar duration, matching frame hashes). The
// best copy of each group is recommended to keep. Nothing is deleted here.

// MaxImageDistance bounds the image pHash distance.
const MaxImageDistance = 11

// DupeOptions control a duplicate scan. JSON names are the protocol's;
// omitted fields take their defaults.
type DupeOptions struct {
	// Scope: only files matching this query ("" = everything).
	Scope               string `json:"scope"`
	MinSize             uint64 `json:"min_size"`
	Exact               bool   `json:"exact"`
	Images              bool   `json:"images"`
	Videos              bool   `json:"videos"`
	ImageDistance       int    `json:"image_distance"`
	VideoDistance       int    `json:"video_distance"`
	DurationToleranceMS uint64 `json:"duration_tolerance_ms"`
}

// DefaultDupeOptions: every kind, image distance 7.
func DefaultDupeOptions() DupeOptions {
	return DupeOptions{MinSize: 1, Exact: true, Images: true, Videos: true, ImageDistance: 7, VideoDistance: 12, DurationToleranceMS: 2000}
}

// UnmarshalJSON fills omitted fields with defaults.
func (o *DupeOptions) UnmarshalJSON(b []byte) error {
	type plain DupeOptions
	p := plain(DefaultDupeOptions())
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*o = DupeOptions(p)
	return nil
}

// Dupe group kinds.
const (
	DupeExact          = "exact"
	DupeSimilarImage   = "similar_image"
	DupeReencodedVideo = "reencoded_video"
)

// DupeMember is one copy.
type DupeMember struct {
	Path       string   `json:"path"`
	Size       uint64   `json:"size"`
	Modified   int64    `json:"modified"`
	Volume     *string  `json:"volume"`
	Width      *uint32  `json:"width"`
	Height     *uint32  `json:"height"`
	HDR        bool     `json:"hdr"`
	BPP        *float32 `json:"bpp"`
	Channels   *uint8   `json:"channels"`
	Bitrate    *uint32  `json:"bitrate"`
	DurationMS *uint64  `json:"duration_ms"`
	// Keep: the recommended copy to keep.
	Keep    bool   `json:"keep"`
	Summary string `json:"summary"`
}

// DupeGroup is a set of copies.
type DupeGroup struct {
	Kind        string       `json:"kind"`
	Members     []DupeMember `json:"members"`
	Reclaimable uint64       `json:"reclaimable"`
}

// DupeProgress: how far a duplicate scan has got. Phase is "listing"
// (choosing the files to compare), "quick" (reading the start of
// same-size files), "full" (reading whole files whose starts match),
// "images" (looking at pictures), "videos" (looking at video frames) or
// "saving"; Done of Total files for the reading phases.
type DupeProgress struct {
	Phase string `json:"phase"`
	Done  uint64 `json:"done"`
	Total uint64 `json:"total"`
}

// DupeProgress: the duplicate scan running now, or nil.
func (ld *Laserdot) DupeProgress() *DupeProgress { return ld.dupes.Load() }

// StopDuplicates asks the running scan to stop early: it stops reading
// files, groups what it has found and saves what it read (so the next scan
// skips those files). False: no scan was running.
func (ld *Laserdot) StopDuplicates() bool {
	if ld.dupes.Load() == nil {
		return false
	}
	ld.dupeStop.Store(true)
	return true
}

// dupeProgressSeen: tests watch the progress reports.
var dupeProgressSeen func(DupeProgress)

// progressFn reports a phase's progress (nil: not reported).
type progressFn func(phase string, done, total uint64)

// DupeReport is the result of a scan.
type DupeReport struct {
	Groups       []DupeGroup `json:"groups"`
	Reclaimable  uint64      `json:"reclaimable"`
	Files        uint64      `json:"files"`
	Read         uint64      `json:"read"`
	BytesRead    uint64      `json:"bytes_read"`
	Unreadable   uint64      `json:"unreadable"`
	VideoDecoder *string     `json:"video_decoder"`
	MS           uint64      `json:"ms"`
	// Stopped: the scan was stopped early (StopDuplicates); Groups hold what
	// it had found. What it read is saved, so the next scan continues.
	Stopped bool `json:"stopped"`
}

type cand struct {
	path, key, ext string
	source         int
	volume         *string
	inode, size    uint64
	mtime          int64
	kind           media.Kind
	w, h           uint32
	hasDims        bool
	quality        index.Quality
	hasQuality     bool
}

type sets []int

func (s sets) find(i int) int {
	for s[i] != i {
		s[i] = s[s[i]]
		i = s[i]
	}
	return i
}

func (s sets) union(a, b int) {
	a, b = s.find(a), s.find(b)
	if a != b {
		s[max(a, b)] = min(a, b)
	}
}

// FindDuplicates scans for duplicates. Fingerprints are cached in this
// Mac's index, so a second scan reads only what changed.
func (ld *Laserdot) FindDuplicates(opts DupeOptions) (*DupeReport, error) {
	t := time.Now()
	prog := func(phase string, done, total uint64) {
		p := &DupeProgress{Phase: phase, Done: done, Total: total}
		ld.dupes.Store(p)
		if dupeProgressSeen != nil {
			dupeProgressSeen(*p)
		}
	}
	ld.dupeStop.Store(false)
	prog("listing", 0, 0)
	defer ld.dupes.Store(nil)
	stop := ld.dupeStop.Load
	cands, err := ld.dupeCandidates(opts)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(cands, func(a, b cand) int { return strings.Compare(a.path, b.path) })
	type ino struct {
		src  int
		node uint64
	}
	seen := map[ino]bool{}
	cands = slices.DeleteFunc(cands, func(c cand) bool {
		if c.inode == 0 {
			return false
		}
		k := ino{c.source, c.inode}
		dup := seen[k]
		seen[k] = true
		return dup // hard links are one file
	})
	wanted := make([]index.FingerprintKey, len(cands))
	for i, c := range cands {
		wanted[i] = index.FingerprintKey{Key: c.key, Size: c.size, Modified: c.mtime}
	}
	ld.wmu.Lock()
	cached, err := ld.db.Fingerprints(wanted)
	ld.wmu.Unlock()
	if err != nil {
		return nil, err
	}
	fps := make([]index.Fingerprint, len(cands))
	for i, c := range cands {
		if f, ok := cached[c.key]; ok {
			fps[i] = f
		} else {
			fps[i] = index.Fingerprint{Size: c.size, Modified: c.mtime}
		}
	}
	before := slices.Clone(fps)
	rep := &DupeReport{Groups: []DupeGroup{}, Files: uint64(len(cands))}
	if opts.Videos {
		if b := media.VideoBackend(); b != "" {
			rep.VideoDecoder = &b
		}
	}
	s := make(sets, len(cands))
	for i := range s {
		s[i] = i
	}
	exactRoot := make([]int, len(cands))
	for i := range exactRoot {
		exactRoot[i] = -1
	}
	videoEdge := make([]bool, len(cands))
	// Each pass stops reading once asked to; what it read still counts.
	if opts.Exact {
		exactPass(cands, fps, s, exactRoot, rep, prog, stop)
	}
	if opts.Images && !stop() {
		imagePass(cands, fps, s, opts, rep, prog, stop)
	}
	if opts.Videos && rep.VideoDecoder != nil && !stop() {
		videoPass(cands, fps, s, videoEdge, opts, rep, prog, stop)
	}
	rep.Stopped = stop()
	prog("saving", 0, 0)
	var keys []string
	var changed []index.Fingerprint
	for i := range cands {
		if !fps[i].Equal(before[i]) {
			keys = append(keys, cands[i].key)
			changed = append(changed, fps[i])
		}
	}
	ld.wmu.Lock()
	err = ld.db.PutFingerprints(keys, changed)
	ld.wmu.Unlock()
	if err != nil {
		return nil, err
	}
	rep.Read = uint64(len(keys))
	comps := map[int][]int{}
	for i := range cands {
		r := s.find(i)
		comps[r] = append(comps[r], i)
	}
	for _, idx := range comps {
		if len(idx) < 2 {
			continue
		}
		kind := DupeSimilarImage
		first := exactRoot[idx[0]]
		allExact := first >= 0
		for _, i := range idx {
			allExact = allExact && exactRoot[i] == first
		}
		switch {
		case allExact:
			kind = DupeExact
		case slices.ContainsFunc(idx, func(i int) bool { return videoEdge[i] }):
			kind = DupeReencodedVideo
		}
		members := make([]DupeMember, len(idx))
		for k, i := range idx {
			members[k] = member(&cands[i])
		}
		slices.SortFunc(members, func(a, b DupeMember) int {
			if c := cmpRank(b, a); c != 0 {
				return c
			}
			return tie(a, b)
		})
		members[0].Keep = true
		g := DupeGroup{Kind: kind, Members: members}
		for _, m := range members[1:] {
			g.Reclaimable += m.Size
		}
		rep.Reclaimable += g.Reclaimable
		rep.Groups = append(rep.Groups, g)
	}
	slices.SortFunc(rep.Groups, func(a, b DupeGroup) int {
		if a.Reclaimable != b.Reclaimable {
			return cmp.Compare(b.Reclaimable, a.Reclaimable)
		}
		return strings.Compare(a.Members[0].Path, b.Members[0].Path)
	})
	rep.MS = uint64(time.Since(t).Milliseconds())
	return rep, nil
}

func (ld *Laserdot) dupeCandidates(opts DupeOptions) ([]cand, error) {
	var scope *query.Compiled
	if strings.TrimSpace(opts.Scope) != "" {
		pq, err := query.Parse(opts.Scope)
		if err != nil {
			return nil, err
		}
		if scope, err = query.Compile(pq, false); err != nil {
			return nil, err
		}
	}
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	var out []cand
	for n := 0; n <= len(ld.drives); n++ {
		s := ld.store
		var volume *string
		var d *drive
		if n > 0 {
			d = ld.drives[n-1]
			s, volume = d.store, &d.name
		}
		var ids *roaring.Bitmap
		if scope != nil {
			var err error
			if ids, err = s.Execute(scope); err != nil {
				continue // e.g. in: a folder this index lacks
			}
		} else {
			ids = s.Live()
			for _, r := range s.RootIDs() {
				ids.Remove(r)
			}
		}
		it := ids.Iterator()
		for it.HasNext() {
			id := it.Next()
			if s.IsDir(id) || s.IsSymlink(id) || s.IsDataless(id) || s.IsPending(id) || s.Size(id) < max(opts.MinSize, 1) {
				continue
			}
			p := s.Path(id)
			key := p
			if d != nil {
				rel, _ := filepath.Rel(d.mount, p)
				key = "drive:" + d.id + ":" + rel
			}
			c := cand{path: p, key: key, source: n, volume: volume, inode: s.Inode(id), size: s.Size(id),
				mtime: s.Modified(id), kind: s.Kind(id), ext: media.Extension(s.FoldedName(id))}
			c.w, c.h, c.hasDims = s.Dimensions(id)
			c.quality, c.hasQuality = s.Quality(id)
			out = append(out, c)
		}
	}
	return out, nil
}

// compute runs f over the chosen candidates in parallel; f returns the
// bytes read, or ok false for an unreadable file.
func compute(cands []cand, fps []index.Fingerprint, which []int, rep *DupeReport, phase string, prog progressFn,
	stop func() bool, f func(c *cand, fp *index.Fingerprint) (uint64, bool)) {
	type res struct {
		fp   index.Fingerprint
		read uint64
		ok   bool
		done bool // false: not read (the scan was stopped first)
	}
	out := make([]res, len(which))
	total := uint64(len(which))
	var done atomic.Uint64
	if prog != nil {
		prog(phase, 0, total)
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for k, i := range which {
		if stop() {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			fp := fps[i]
			n, ok := f(&cands[i], &fp)
			out[k] = res{fp, n, ok, true}
			if d := done.Add(1); prog != nil {
				prog(phase, d, total)
			}
		}()
	}
	wg.Wait()
	for k, i := range which {
		if !out[k].done {
			continue
		}
		fps[i] = out[k].fp
		if out[k].ok {
			rep.BytesRead += out[k].read
		} else {
			rep.Unreadable++
		}
	}
}

func exactPass(cands []cand, fps []index.Fingerprint, s sets, exactRoot []int, rep *DupeReport, prog progressFn, stop func() bool) {
	bySize := map[uint64][]int{}
	for i, c := range cands {
		bySize[c.size] = append(bySize[c.size], i)
	}
	var needQuick []int
	for _, v := range bySize {
		if len(v) > 1 {
			for _, i := range v {
				if fps[i].Quick == nil {
					needQuick = append(needQuick, i)
				}
			}
		}
	}
	slices.Sort(needQuick)
	compute(cands, fps, needQuick, rep, "quick", prog, stop, func(c *cand, fp *index.Fingerprint) (uint64, bool) {
		h, err := media.QuickHash(c.path, c.size)
		if err != nil {
			return 0, false
		}
		fp.Quick = &h
		if media.QuickIsFull(c.size) {
			fp.Full = &h
		}
		return min(c.size, 3*media.Window), true
	})
	type qk struct {
		size uint64
		h    [32]byte
	}
	byQuick := map[qk][]int{}
	for _, v := range bySize {
		if len(v) > 1 {
			for _, i := range v {
				if fps[i].Quick != nil {
					k := qk{cands[i].size, *fps[i].Quick}
					byQuick[k] = append(byQuick[k], i)
				}
			}
		}
	}
	var needFull []int
	for _, v := range byQuick {
		if len(v) > 1 {
			for _, i := range v {
				if fps[i].Full == nil {
					needFull = append(needFull, i)
				}
			}
		}
	}
	slices.Sort(needFull)
	compute(cands, fps, needFull, rep, "full", prog, stop, func(c *cand, fp *index.Fingerprint) (uint64, bool) {
		h, err := media.FullHash(c.path)
		if err != nil {
			return 0, false
		}
		fp.Full = &h
		return c.size, true
	})
	byFull := map[[32]byte][]int{}
	for _, v := range byQuick {
		if len(v) > 1 {
			for _, i := range v {
				if fps[i].Full != nil {
					byFull[*fps[i].Full] = append(byFull[*fps[i].Full], i)
				}
			}
		}
	}
	for _, v := range byFull {
		if len(v) > 1 {
			slices.Sort(v)
			for _, i := range v {
				exactRoot[i] = v[0]
				s.union(v[0], i)
			}
		}
	}
}

func imagePass(cands []cand, fps []index.Fingerprint, s sets, opts DupeOptions, rep *DupeReport, prog progressFn, stop func() bool) {
	var imgs, need []int
	for i, c := range cands {
		if c.kind == media.Image && media.HashableImage(c.ext) {
			imgs = append(imgs, i)
			if fps[i].Image == nil && fps[i].Flags&index.FPImageFailed == 0 {
				need = append(need, i)
			}
		}
	}
	compute(cands, fps, need, rep, "images", prog, stop, func(c *cand, fp *index.Fingerprint) (uint64, bool) {
		g, ok := media.ImageGray(c.path, c.ext)
		if !ok {
			fp.Flags |= index.FPImageFailed
			return 0, false
		}
		h := media.PHash(&g)
		fp.Image = &h
		if media.IsFlat(&g) {
			fp.Flags |= index.FPImageFlat
		}
		return 0, true
	})
	var hashed []hashedImage
	for _, i := range imgs {
		if fps[i].Flags&index.FPImageFlat == 0 && fps[i].Image != nil {
			hashed = append(hashed, hashedImage{i, *fps[i].Image})
		}
	}
	for _, p := range nearPairs(hashed, min(opts.ImageDistance, MaxImageDistance)) {
		s.union(p[0], p[1])
	}
}

type hashedImage struct {
	id   int
	hash uint64
}

const (
	chunkBits  = 16
	chunkCount = 4
)

func chunkOf(h uint64, c int) int { return int(h >> (c * chunkBits) & 0xFFFF) }

func neighbours(k int, r int) []int {
	v := []int{k}
	if r >= 1 {
		for a := 0; a < chunkBits; a++ {
			v = append(v, k^(1<<a))
			if r >= 2 {
				for b := a + 1; b < chunkBits; b++ {
					v = append(v, k^(1<<a)^(1<<b))
				}
			}
		}
	}
	return v
}

// nearPairs: every pair within max bits, by multi-index hashing — a pair
// within max differs in at most max/4 bits in at least one 16-bit chunk.
func nearPairs(hashed []hashedImage, maxD int) [][2]int {
	maxD = min(maxD, MaxImageDistance)
	if maxD < 0 {
		return nil
	}
	r := maxD / chunkCount
	type table struct {
		start []uint32
		ids   []uint32
	}
	tables := make([]table, chunkCount)
	for c := range tables {
		start := make([]uint32, 1<<chunkBits+1)
		for _, h := range hashed {
			start[chunkOf(h.hash, c)+1]++
		}
		for i := 1; i < len(start); i++ {
			start[i] += start[i-1]
		}
		fill := slices.Clone(start)
		ids := make([]uint32, len(hashed))
		for n, h := range hashed {
			k := chunkOf(h.hash, c)
			ids[fill[k]] = uint32(n)
			fill[k]++
		}
		tables[c] = table{start, ids}
	}
	chunkClose := func(a, b uint64, c int) bool { return bits.OnesCount(uint(chunkOf(a, c)^chunkOf(b, c))) <= r }
	var pairs [][2]int
	for n, h := range hashed {
		for c, tb := range tables {
			for _, k := range neighbours(chunkOf(h.hash, c), r) {
				for _, m := range tb.ids[tb.start[k]:tb.start[k+1]] {
					hm := hashed[m].hash
					if int(m) <= n || media.Hamming(h.hash, hm) > maxD {
						continue
					}
					// Found through an earlier chunk already.
					dupe := false
					for e := 0; e < c; e++ {
						dupe = dupe || chunkClose(h.hash, hm, e)
					}
					if !dupe {
						pairs = append(pairs, [2]int{h.id, hashed[m].id})
					}
				}
			}
		}
	}
	slices.SortFunc(pairs, func(a, b [2]int) int {
		if a[0] != b[0] {
			return a[0] - b[0]
		}
		return a[1] - b[1]
	})
	return pairs
}

func videoPass(cands []cand, fps []index.Fingerprint, s sets, videoEdge []bool, opts DupeOptions, rep *DupeReport, prog progressFn, stop func() bool) {
	dur := func(i int) uint64 { return uint64(cands[i].quality.DurationMS) }
	var vids []int
	for i, c := range cands {
		if c.kind == media.Video && c.hasQuality && c.quality.HasVideo && dur(i) > 0 {
			vids = append(vids, i)
		}
	}
	slices.SortStableFunc(vids, func(a, b int) int { return cmp.Compare(dur(a), dur(b)) })
	var pairs [][2]int
	for n, a := range vids {
		for _, b := range vids[n+1:] {
			if dur(b)-dur(a) > opts.DurationToleranceMS {
				break
			}
			pairs = append(pairs, [2]int{a, b})
		}
	}
	var need []int
	for _, p := range pairs {
		for _, i := range p {
			if fps[i].Frames == nil && fps[i].Flags&index.FPVideoFailed == 0 {
				need = append(need, i)
			}
		}
	}
	slices.Sort(need)
	need = slices.Compact(need)
	compute(cands, fps, need, rep, "videos", prog, stop, func(c *cand, fp *index.Fingerprint) (uint64, bool) {
		frames, err := media.VideoFrames(c.path, uint64(c.quality.DurationMS))
		if err != nil {
			fp.Flags |= index.FPVideoFailed
			return 0, false
		}
		var h [3]uint64
		var flat uint8
		for k := 0; k < min(3, len(frames)); k++ {
			h[k] = media.PHash(&frames[k])
			if media.IsFlat(&frames[k]) {
				flat |= 1 << k
			}
		}
		fp.Frames, fp.Flat = &h, flat
		return 0, true
	})
	for _, p := range pairs {
		a, b := fps[p[0]], fps[p[1]]
		if a.Frames != nil && b.Frames != nil && framesMatch(*a.Frames, a.Flat, *b.Frames, b.Flat, opts.VideoDistance) {
			s.union(p[0], p[1])
			videoEdge[p[0]], videoEdge[p[1]] = true, true
		}
	}
}

// framesMatch: flat frames only match flat frames, and at least one
// detailed frame pair must be close.
func framesMatch(a [3]uint64, af uint8, b [3]uint64, bf uint8, maxD int) bool {
	detailed := 0
	for k := 0; k < 3; k++ {
		fa, fb := af&(1<<k) != 0, bf&(1<<k) != 0
		switch {
		case fa && fb:
		case !fa && !fb && media.Hamming(a[k], b[k]) <= maxD:
			detailed++
		default:
			return false
		}
	}
	return detailed > 0
}

func member(c *cand) DupeMember {
	q := c.quality
	m := DupeMember{Path: c.path, Size: c.size, Modified: c.mtime, Volume: c.volume, HDR: q.HDR}
	var parts []string
	if c.hasDims {
		w, h := c.w, c.h
		m.Width, m.Height = &w, &h
		parts = append(parts, fmt.Sprintf("%d×%d", w, h))
	}
	if q.HDR {
		parts = append(parts, "HDR")
	}
	pixels := float64(c.w) * float64(c.h)
	switch {
	case q.HasVideo && q.BPP > 0:
		b := q.BPP
		m.BPP = &b
	case !q.HasVideo && c.hasDims && pixels > 0 && c.kind == media.Image:
		b := float32(float64(c.size) * 8 / pixels)
		m.BPP = &b
	}
	if m.BPP != nil {
		parts = append(parts, fmt.Sprintf("%.2f bpp", *m.BPP))
	}
	if q.Channels > 0 {
		ch := q.Channels
		m.Channels = &ch
		if n := index.ChannelName(ch); n != "" {
			parts = append(parts, n)
		} else {
			parts = append(parts, fmt.Sprintf("%dch", ch))
		}
	}
	if q.Bitrate > 0 {
		b := q.Bitrate
		m.Bitrate = &b
		if !q.HasVideo {
			parts = append(parts, fmt.Sprintf("%d kb/s", b/1000))
		}
	}
	if q.DurationMS > 0 {
		d := uint64(q.DurationMS)
		m.DurationMS = &d
	}
	m.Summary = strings.Join(parts, " · ")
	return m
}

// cmpRank compares by quality: pixels, HDR, bits per pixel, channels,
// bitrate.
func cmpRank(a, b DupeMember) int {
	val := func(p *uint32) uint64 {
		if p == nil {
			return 0
		}
		return uint64(*p)
	}
	if c := cmp.Compare(val(a.Width)*val(a.Height), val(b.Width)*val(b.Height)); c != 0 {
		return c
	}
	if a.HDR != b.HDR {
		if a.HDR {
			return 1
		}
		return -1
	}
	f := func(p *float32) float64 {
		if p == nil {
			return 0
		}
		return float64(*p)
	}
	if c := cmp.Compare(f(a.BPP), f(b.BPP)); c != 0 && !math.IsNaN(f(a.BPP)) {
		return c
	}
	ch := func(p *uint8) uint8 {
		if p == nil {
			return 0
		}
		return *p
	}
	if c := cmp.Compare(ch(a.Channels), ch(b.Channels)); c != 0 {
		return c
	}
	return cmp.Compare(val(a.Bitrate), val(b.Bitrate))
}

// tie: this Mac before a drive, then older, then shorter path.
func tie(a, b DupeMember) int {
	if (a.Volume != nil) != (b.Volume != nil) {
		if a.Volume == nil {
			return -1
		}
		return 1
	}
	if c := cmp.Compare(a.Modified, b.Modified); c != 0 {
		return c
	}
	if c := cmp.Compare(len(a.Path), len(b.Path)); c != 0 {
		return c
	}
	return strings.Compare(a.Path, b.Path)
}
