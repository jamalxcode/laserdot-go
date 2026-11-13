package index

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/fold"
	"github.com/jamalxcode/laserdot-go/internal/media"
	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// IndexStats reports an index run. JSON names are the daemon protocol's.
type IndexStats struct {
	Root        string `json:"root"`
	Files       uint64 `json:"files"`
	Folders     uint64 `json:"folders"`
	Probed      uint64 `json:"probed"` // media headers read this run
	Reused      uint64 `json:"reused"` // earlier probes reused (same size + mtime)
	ProbeFailed uint64 `json:"probe_failed"`
	Dataless    uint64 `json:"dataless"` // iCloud placeholders, never opened
	ScanErrors  uint64 `json:"scan_errors"`
	ScanMS      uint64 `json:"scan_ms"`
	ProbeMS     uint64 `json:"probe_ms"`
	WriteMS     uint64 `json:"write_ms"`
	// Incremental: a refresh of an indexed folder rather than a first index.
	Incremental bool   `json:"incremental"`
	Added       uint64 `json:"added"`
	Removed     uint64 `json:"removed"`
	Renamed     uint64 `json:"renamed"`
	Modified    uint64 `json:"modified"`
	Pending     uint64 `json:"pending"`
}

// BulkOptions tune a first index.
type BulkOptions struct {
	// Chunk: entries per committed chunk (each is a checkpoint, §9.7).
	Chunk int
	// StopAfterChunks > 0 stops after that many chunks (simulates a drive
	// unplugged mid-index).
	StopAfterChunks int
	// Progress, when set, hears the phase ("scanning", then "saving") and,
	// while saving, how many of the found entries are done.
	Progress func(phase string, done, total uint64)
	// Raw, when set, receives the root's entries as Load would return them
	// (so the caller can build the in-memory store without reading them back).
	Raw *[]RawEntry
	// Errors, when set, receives what could not be read.
	Errors *[]scan.Error
	// ProbeLater: do not read media headers now; store those files as
	// pending (the caller reads them afterwards), so files are searchable
	// as soon as they are listed. Opening hundreds of thousands of files is
	// most of a first index on macOS.
	ProbeLater bool
}

// ErrInterrupted: indexing stopped before the end (StopAfterChunks).
var ErrInterrupted = errors.New("indexing interrupted")

// wantsProbe: an entry whose header a probe would read.
func wantsProbe(e *scan.Entry) bool {
	if e.IsDir || e.Dataless || e.IsSymlink || e.Parent < 0 {
		return false
	}
	ext := media.Extension(fold.Fold(filepath.Base(e.Path)))
	return media.WantsProbe(media.Classify(ext, false, e.Size), ext)
}

// probeEntry reads one entry's header unless it must not be read.
func probeEntry(e *scan.Entry) Probe {
	if e.IsDir || e.Dataless || e.IsSymlink || e.Parent < 0 || ShouldDefer(e) {
		return Probe{}
	}
	ext := media.Extension(fold.Fold(filepath.Base(e.Path)))
	kind := media.Classify(ext, false, e.Size)
	if !media.WantsProbe(kind, ext) {
		return Probe{}
	}
	st, rec := media.Probe(e.Path, kind, ext, e.Size)
	return Probe{Status: st, Record: rec}
}

// probeAll probes entries in parallel; reuse may supply an earlier result.
func probeAll(entries []scan.Entry, reuse func(e *scan.Entry) (Probe, bool), later bool, probed, reused, failed *atomic.Uint64) []Probe {
	out := make([]Probe, len(entries))
	parallelChunks(len(entries), 64, func(i int) {
		e := &entries[i]
		if reuse != nil && !e.IsDir {
			if p, ok := reuse(e); ok {
				reused.Add(1)
				out[i] = p
				return
			}
		}
		if later {
			if wantsProbe(e) {
				out[i] = Probe{Later: true}
			}
			return
		}
		p := probeEntry(e)
		if p.Status != media.ProbeSkipped {
			probed.Add(1)
			if p.Status == media.ProbeFailed {
				failed.Add(1)
			}
		}
		out[i] = p
	})
	return out
}

// IndexRoot indexes root from scratch (the bulk path for a folder not
// indexed yet; indexed folders inside it are absorbed). Unchanged media
// files keep their earlier probes.
func IndexRoot(db *Db, root string, opts scan.Options, bulk BulkOptions) (IndexStats, error) {
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return IndexStats{}, fmt.Errorf("%s is not a folder", root)
	}
	roots, err := db.Roots()
	if err != nil {
		return IndexStats{}, err
	}
	for _, r := range roots {
		if within(root, r.Path) {
			root = r.Path
			break
		}
	}
	st := IndexStats{Root: root}
	progress := func(phase string, done, total uint64) {
		if bulk.Progress != nil {
			bulk.Progress(phase, done, total)
		}
	}
	progress("scanning", 0, 0)
	t := time.Now()
	// While walking: how many files were found so far, four times a second
	// (a big folder takes a while; the count shows it is not stuck).
	var found atomic.Uint64
	opts.Found = &found
	walked, ticked := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(ticked)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-walked:
				return
			case <-tick.C:
				progress("scanning", found.Load(), 0)
			}
		}
	}()
	entries, errs, err := scan.Walk(root, opts)
	close(walked)
	<-ticked // no "scanning" report after the next phase's
	if err != nil {
		return st, err
	}
	st.ScanMS = uint64(time.Since(t).Milliseconds())
	st.ScanErrors = uint64(len(errs))
	prior, err := db.PriorProbes(root)
	if err != nil {
		return st, err
	}
	reuse := func(e *scan.Entry) (Probe, bool) {
		p, ok := prior[e.Path]
		if ok && p.Size == e.Size && p.Modified == e.Modified && !ShouldDefer(e) {
			return p.Probe, true
		}
		return Probe{}, false
	}
	var probed, reusedN, failed atomic.Uint64
	rootID, base, err := db.BeginRoot(root)
	if err != nil {
		return st, err
	}
	progress("saving", 0, uint64(len(entries)))
	chunk := max(bulk.Chunk, 1)
	if bulk.Chunk == 0 {
		chunk = 20_000
	}
	if bulk.Errors != nil {
		*bulk.Errors = errs
	}
	var raw []RawEntry
	if bulk.Raw != nil {
		raw = make([]RawEntry, len(entries))
	}
	// Probing (parallel reads) of the next chunk overlaps writing this one.
	type probedChunk struct {
		start, end int
		probes     []Probe
		ms         uint64
	}
	ready := make(chan probedChunk, 1)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		defer close(ready)
		for start := 0; start < len(entries); start += chunk {
			end := min(start+chunk, len(entries))
			t := time.Now()
			ps := probeAll(entries[start:end], reuse, bulk.ProbeLater, &probed, &reusedN, &failed)
			select {
			case ready <- probedChunk{start, end, ps, uint64(time.Since(t).Milliseconds())}:
			case <-stop:
				return
			}
		}
	}()
	n := 0
	for c := range ready {
		if bulk.StopAfterChunks > 0 && n >= bulk.StopAfterChunks {
			return st, ErrInterrupted
		}
		n++
		st.ProbeMS += c.ms
		t := time.Now()
		if err := db.InsertChunk(rootID, base, entries, c.start, c.probes); err != nil {
			return st, err
		}
		st.WriteMS += uint64(time.Since(t).Milliseconds())
		if raw != nil {
			for i := c.start; i < c.end; i++ {
				r := rawOf(&entries[i], c.probes[i-c.start], base+int64(i))
				r.Parent = entries[i].Parent
				if r.Parent < 0 {
					r.Name = root
				}
				raw[i] = r
			}
		}
		progress("saving", uint64(c.end), uint64(len(entries)))
	}
	if err := db.FinishRoot(root); err != nil {
		return st, err
	}
	for i := range entries[1:] {
		e := &entries[i+1]
		if e.IsDir {
			st.Folders++
		} else {
			st.Files++
		}
		if e.Dataless {
			st.Dataless++
		}
		if ShouldDefer(e) {
			st.Pending++
		}
	}
	st.Probed, st.Reused, st.ProbeFailed = probed.Load(), reusedN.Load(), failed.Load()
	if bulk.Raw != nil {
		*bulk.Raw = raw
	}
	return st, nil
}
func within(path, root string) bool {
	sep := string(os.PathSeparator)
	return path == root || len(path) > len(root) && path[:len(root)] == root &&
		(path[len(root):len(root)+1] == sep || root == sep)
}

// parallelChunks runs f(0..n-1) on all cores in chunks of at least min.
func parallelChunks(n, minChunk int, f func(int)) {
	workers := max(1, min(n/max(minChunk, 1), 4*numCPU()))
	if workers <= 1 {
		for i := 0; i < n; i++ {
			f(i)
		}
		return
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < min(workers, numCPU()*2); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				s := int(next.Add(int64(minChunk))) - minChunk
				if s >= n {
					return
				}
				for i := s; i < min(s+minChunk, n); i++ {
					f(i)
				}
			}
		}()
	}
	wg.Wait()
}
