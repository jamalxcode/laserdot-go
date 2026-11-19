package index

import (
	"container/heap"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// Streaming indexing: a first index for drives of any size, in bounded
// memory, resumable after any interruption.
//
//   - Folders are the unit of work. A folder's contents and the folder's
//     change from "unlisted" to listed are written in one transaction, so
//     the index itself is the checkpoint: after a crash, an unplugged drive
//     or a restart, the folders still marked unlisted are exactly the work
//     left (none is half done, none is lost, nothing is written twice).
//     The mark is a value in an existing column, so the drive's index keeps
//     the same schema and still opens in older versions.
//   - Memory is bounded: at most MaxQueue folders wait in memory; the rest
//     wait in the index, marked unlisted, and are read back as the queue
//     drains. Rows are written in batches of BatchRows. Nothing grows with
//     the size of the drive except the index on disk.
//   - Workers take folders from one shared queue (a slow or huge folder
//     never holds the others up), and their number adapts to the measured
//     speed: more while it helps, fewer when they slow each other down (a
//     spinning disk is fastest with few).
//   - The queue is ordered by priority: shallow folders first, recently
//     changed ones and those the user is searching in before the rest, and
//     the longer a folder waits the higher it rises, so none is starved.

// codeUnlisted marks a folder saved but not read yet (its contents are not
// in the index). Only folders carry it; files' probe codes are unchanged.
const codeUnlisted = 4

// StreamOptions tune a streaming index.
type StreamOptions struct {
	// Progress hears the number of entries saved so far.
	Progress func(saved uint64)
	// OnBatch hears each committed batch (a live in-memory index can grow
	// with it): rows in insert order, each with its parent's database id.
	OnBatch func(rows []StreamRow)
	// Priority: extra priority for a folder (higher first), e.g. one the
	// user is searching in. Called often: keep it cheap.
	Priority func(path string) float64
	// PriorityVersion changes when Priority's answers do (a new search):
	// the waiting folders are then ordered again.
	PriorityVersion func() uint64
	// MaxQueue: folders kept waiting in memory (default 20,000).
	MaxQueue int
	// BatchRows: rows per transaction (default 5,000).
	BatchRows int
	// MaxWorkers: the most folders read at once (default 16; it starts at 2
	// and adapts).
	MaxWorkers int
	// StopAfterBatches > 0: stop after that many batches (tests: an
	// unplugged drive).
	StopAfterBatches int
	// Stop, when it answers true, ends the run early (resumable).
	Stop func() bool
	// Errors receives what could not be read.
	Errors *[]scan.Error
	// OnStart runs once the root's row is saved (or an earlier run is found)
	// and before any folder is read: the moment to load the index so far.
	OnStart func() error
}

// StreamRow is one saved entry.
type StreamRow struct {
	ID, Parent int64
	Raw        RawEntry
}

// StreamStats: what a streaming run did.
type StreamStats struct {
	IndexStats
	Resumed bool `json:"resumed"`
	// Workers: how many readers it ended with (adaptive).
	Workers int `json:"workers"`
	// Unfinished: folders that did not answer (a timeout, a read error)
	// and stay unlisted; the root stays incomplete, and the next run reads
	// them again.
	Unfinished int `json:"unfinished"`
}

type streamDir struct {
	id       int64
	path     string
	depth    int
	modified int64
	queued   time.Time
	prio     float64
}

// IndexStreaming indexes root into db, or goes on with an earlier run that
// did not finish (root's row is incomplete and has unlisted folders). Use
// for drives: the in-memory index is not built here (see OnBatch).
func IndexStreaming(db *Db, root string, opts scan.Options, so StreamOptions) (StreamStats, error) {
	if so.MaxQueue <= 0 {
		so.MaxQueue = 20_000
	}
	if so.BatchRows <= 0 {
		so.BatchRows = 5_000
	}
	if so.MaxWorkers <= 0 {
		so.MaxWorkers = 16
	}
	st := StreamStats{}
	st.Root = root
	r := &streamRun{db: db, root: root, opts: opts, so: so, st: &st, inFlight: map[int64]bool{}, failed: map[int64]bool{}}
	if err := r.start(); err != nil {
		return st, err
	}
	if so.OnStart != nil {
		if err := so.OnStart(); err != nil {
			return st, err
		}
	}
	err := r.run()
	st.Workers = int(r.target.Load())
	st.Unfinished = len(r.failed)
	return st, err
}

type streamRun struct {
	db     *Db
	root   string
	opts   scan.Options
	so     StreamOptions
	st     *StreamStats
	rootID int64
	nextID int64

	mu       sync.Mutex
	queue    dirHeap
	inFlight map[int64]bool // folders handed to a worker, not yet written
	failed   map[int64]bool // folders that did not answer this run (left unlisted)
	more     bool           // unlisted folders may wait in the index beyond the queue
	links    map[[2]uint64]bool
	target   atomic.Int64 // workers wanted now
	saved    atomic.Uint64
}

// start: a fresh index (root row + root folder, unlisted), or a resume.
func (r *streamRun) start() error {
	d := r.db
	stored := d.stored(r.root)
	var id int64
	var complete bool
	err := d.conn.QueryRow("SELECT id, complete FROM roots WHERE path = ?", stored).Scan(&id, &complete)
	if err == nil && !complete {
		var n int
		if err := d.conn.QueryRow("SELECT COUNT(*) FROM entries WHERE root = ? AND is_dir = 1 AND probe = ?", id, codeUnlisted).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			r.rootID = id
			r.st.Resumed = true
			if err := d.conn.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM entries").Scan(&r.nextID); err != nil {
				return err
			}
			r.more = true
			return r.refill()
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	rootID, base, err := d.BeginRoot(r.root)
	if err != nil {
		return err
	}
	info, err := scan.Stat(r.root)
	if err != nil {
		return err
	}
	r.rootID, r.nextID = rootID, base+1
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(insertSQL, base, rootID, nil, stored, true, false, false, 0, info.Modified, info.Created,
		int64(info.Inode), int64(info.Dev), codeUnlisted, 0, 0, nil, nil); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	r.push(streamDir{id: base, path: r.root, depth: 0, modified: info.Modified})
	return nil
}

func (r *streamRun) prio(d *streamDir) float64 {
	// Shallow first; changed in the last 30 days, or being searched in,
	// earlier; waiting raises it (a minute of waiting is worth one level).
	p := -float64(d.depth)
	if time.Now().Unix()-d.modified < 30*86_400 {
		p += 3
	}
	if r.so.Priority != nil {
		p += r.so.Priority(d.path)
	}
	return p
}

func (r *streamRun) push(d streamDir) {
	d.queued = time.Now()
	d.prio = r.prio(&d)
	heap.Push(&r.queue, d)
}

// refill reads waiting folders back from the index (call with mu held or
// before workers start).
func (r *streamRun) refill() error {
	rows, err := r.db.conn.Query(`SELECT id, mtime FROM entries WHERE root = ? AND is_dir = 1 AND probe = ? ORDER BY id LIMIT ?`,
		r.rootID, codeUnlisted, r.so.MaxQueue+len(r.inFlight)+len(r.failed))
	if err != nil {
		return err
	}
	type idm struct{ id, mtime int64 }
	var found []idm
	for rows.Next() {
		var x idm
		if err := rows.Scan(&x.id, &x.mtime); err != nil {
			rows.Close()
			return err
		}
		found = append(found, x)
	}
	rows.Close()
	queued := map[int64]bool{}
	for _, q := range r.queue {
		queued[q.id] = true
	}
	added := 0
	for _, x := range found {
		if r.inFlight[x.id] || queued[x.id] || r.failed[x.id] {
			continue
		}
		p, depth, err := r.pathOf(x.id)
		if err != nil {
			continue // a name that is not a plain file name: left alone
		}
		r.push(streamDir{id: x.id, path: p, depth: depth, modified: x.mtime})
		added++
	}
	r.more = len(found) >= r.so.MaxQueue+len(r.inFlight)+len(r.failed)
	return nil
}

// pathOf rebuilds a folder's path from its ancestors' names.
func (r *streamRun) pathOf(id int64) (string, int, error) {
	rows, err := r.db.conn.Query(`WITH RECURSIVE up(id, parent, name, depth) AS (
		SELECT id, parent, name, 0 FROM entries WHERE id = ?
		UNION ALL SELECT e.id, e.parent, e.name, up.depth + 1 FROM entries e JOIN up ON e.id = up.parent)
		SELECT name FROM up ORDER BY depth DESC`, id)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return "", 0, err
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return "", 0, errors.New("folder not found")
	}
	first := names[0]
	if r.db.mount == "" {
		return filepath.Join(append([]string{first}, names[1:]...)...), len(names) - 1, nil
	}
	p, ok := r.db.unstored(strings.Join(names, "/"))
	if !ok {
		return "", 0, errors.New("unsafe name")
	}
	return p, len(names) - 1, nil
}

// listed: one folder read by a worker.
type listed struct {
	dir     streamDir
	entries []scan.Entry
	probes  []Probe
	errs    []scan.Error
	// keep: the folder did not answer (a timeout, a read error): it stays
	// unlisted, not empty. gone: the drive itself went away.
	keep, gone bool
}

func (r *streamRun) run() error {
	r.links = map[[2]uint64]bool{}
	r.target.Store(int64(min(2, r.so.MaxWorkers)))
	work := make(chan streamDir)
	results := make(chan listed, 64)
	stop := make(chan struct{})
	var stopOnce sync.Once
	halt := func() { stopOnce.Do(func() { close(stop) }) }
	var workers sync.WaitGroup
	var active atomic.Int64

	// Workers: as many as target says (checked before each folder).
	for w := 0; w < r.so.MaxWorkers; w++ {
		workers.Add(1)
		go func(n int) {
			defer workers.Done()
			for {
				for int64(n) >= r.target.Load() {
					select {
					case <-stop:
						return
					case <-time.After(50 * time.Millisecond):
					}
				}
				select {
				case <-stop:
					return
				case d, ok := <-work:
					if !ok {
						return
					}
					active.Add(1)
					results <- r.read(d)
					active.Add(-1)
				}
			}
		}(w)
	}

	// The dispatcher hands out folders by priority; the writer saves them.
	var writeErr error
	pending := 0 // folders handed out and not yet written
	var batch []listed
	rows := 0
	batches := 0
	lastSaved, lastTune := uint64(0), time.Now()
	lastRate, raised := 0.0, false
	var prioVersion uint64
	if r.so.PriorityVersion != nil {
		prioVersion = r.so.PriorityVersion()
	}
	flush := func() bool {
		if len(batch) == 0 {
			return true
		}
		if err := r.write(batch); err != nil {
			writeErr = err
			return false
		}
		pending -= len(batch)
		batch, rows = batch[:0], 0
		batches++
		if r.so.StopAfterBatches > 0 && batches >= r.so.StopAfterBatches {
			writeErr = ErrInterrupted
			return false
		}
		return true
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if r.so.Stop != nil && r.so.Stop() {
			writeErr = ErrInterrupted
			break
		}
		// Hand out the best waiting folder if a worker is free.
		r.mu.Lock()
		if r.queue.Len() == 0 && r.more && writeErr == nil {
			if err := r.refill(); err != nil {
				writeErr = err
			}
		}
		var next *streamDir
		if r.queue.Len() > 0 {
			d := heap.Pop(&r.queue).(streamDir)
			next = &d
		}
		r.mu.Unlock()
		if writeErr != nil {
			break
		}
		if next == nil && pending == 0 && len(results) == 0 {
			if !flush() {
				break
			}
			r.mu.Lock()
			done := r.queue.Len() == 0 && !r.more
			if done && r.unlisted() > len(r.failed) {
				// The index has the last word: folders still unlisted
				// there are not done, whatever the queue thought.
				r.more, done = true, false
			}
			r.mu.Unlock()
			if done {
				break
			}
			continue
		}
		var out chan streamDir
		if next != nil {
			out = work
		}
		select {
		case out <- *nextOrZero(next):
			r.mu.Lock()
			r.inFlight[next.id] = true
			r.mu.Unlock()
			pending++
			continue
		case res := <-results:
			if next != nil { // not handed out: back in the queue
				r.mu.Lock()
				heap.Push(&r.queue, *next)
				r.mu.Unlock()
			}
			batch = append(batch, res)
			rows += len(res.entries)
			if res.gone {
				// The drive went away: what was read is saved, the rest
				// waits for its return.
				if flush() {
					writeErr = ErrInterrupted
				}
				break
			}
			if rows >= r.so.BatchRows && !flush() {
				break
			}
		case <-tick.C:
			if next != nil {
				r.mu.Lock()
				heap.Push(&r.queue, *next)
				r.mu.Unlock()
			}
			if rows > 0 && !flush() {
				break
			}
		}
		if writeErr != nil {
			break
		}
		// A new search: the folders it is about move up the queue.
		if r.so.PriorityVersion != nil {
			if v := r.so.PriorityVersion(); v != prioVersion {
				prioVersion = v
				r.mu.Lock()
				for i := range r.queue {
					r.queue[i].prio = r.prio(&r.queue[i])
				}
				heap.Init(&r.queue)
				r.mu.Unlock()
			}
		}
		// Every two seconds, tune the number of workers to the speed seen:
		// one more while that helped, one fewer when it did not.
		if time.Since(lastTune) >= 2*time.Second {
			saved := r.saved.Load()
			rate := float64(saved-lastSaved) / time.Since(lastTune).Seconds()
			t := r.target.Load()
			switch {
			case raised && rate < lastRate*0.95 && t > 1:
				r.target.Store(t - 1)
				raised = false
			case rate >= lastRate*0.95 && t < int64(r.so.MaxWorkers) && active.Load() >= t:
				r.target.Store(t + 1)
				raised = true
			default:
				raised = false
			}
			lastSaved, lastTune, lastRate = saved, time.Now(), rate
		}
	}
	halt()
	close(work)
	// Workers still reading (a stuck drive can take its folder time limit)
	// finish on their own; their results are dropped (the folders stay
	// unlisted and are read again next time). Stopping never waits for them.
	go func() {
		workers.Wait()
		close(results)
	}()
	go func() {
		for range results {
		}
	}()
	if writeErr != nil {
		return writeErr
	}
	if len(r.failed) > 0 {
		return nil // usable, but incomplete: the next run reads the rest
	}
	return r.db.FinishRoot(r.root)
}

func nextOrZero(d *streamDir) *streamDir {
	if d == nil {
		return &streamDir{}
	}
	return d
}

// read lists one folder and probes its files (a worker).
func (r *streamRun) read(d streamDir) listed {
	out := listed{dir: d}
	if scan.IsOpaque(d.path, r.opts) {
		return out // a package (an app…) is one entry: nothing inside
	}
	entries, err := scan.ListDir(d.path, r.opts)
	if err != nil {
		out.errs = []scan.Error{{Path: d.path, Message: err.Error()}}
		switch {
		case errors.Is(err, fs.ErrPermission):
			// Readable never: listed (and reported as unreadable).
		case !exists(r.root):
			out.keep, out.gone = true, true
		case errors.Is(err, fs.ErrNotExist):
			// Deleted since its parent was read: nothing to list.
		default:
			out.keep = true // didn't answer: not the same as empty
		}
		return out
	}
	out.entries = entries
	out.probes = make([]Probe, len(entries))
	probe := func(i int) {
		e := entries[i]
		e.Parent = 0 // inside a folder: probed like any listed file
		out.probes[i] = probeEntry(&e)
	}
	if len(entries) > 64 {
		parallelChunks(len(entries), 64, probe) // a folder full of videos
	} else {
		for i := range entries {
			probe(i)
		}
	}
	return out
}

// write saves folders read: their entries, child folders marked unlisted,
// and each folder marked listed, in one transaction.
func (r *streamRun) write(batch []listed) error {
	d := r.db
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := invalidateSnapshot(tx); err != nil {
		return err
	}
	ins, err := tx.Prepare(insertSQL)
	if err != nil {
		return err
	}
	defer ins.Close()
	done, err := tx.Prepare("UPDATE entries SET probe = ? WHERE id = ?")
	if err != nil {
		return err
	}
	defer done.Close()
	var saved []StreamRow
	var children []streamDir
	r.mu.Lock()
	for _, l := range batch {
		for i := range l.entries {
			e := &l.entries[i]
			// Hard links are one file.
			if !e.IsDir && e.Nlink > 1 {
				k := [2]uint64{e.Dev, e.Inode}
				if r.links[k] {
					continue
				}
				r.links[k] = true
			}
			id := r.nextID
			r.nextID++
			p := l.probes[i]
			code, w, h, blob := storedCode(e, p)
			descend := e.IsDir && !e.IsSymlink && !scan.IsOpaque(e.Path, r.opts) && !scan.IsMountPoint(e.Path)
			if descend {
				code = codeUnlisted
			}
			if _, err := ins.Exec(id, r.rootID, l.dir.id, filepath.Base(e.Path), e.IsDir, e.IsSymlink, e.Dataless,
				int64(e.Size), e.Modified, e.Created, int64(e.Inode), int64(e.Dev), code, w, h, blob, tagsText(e.Tags)); err != nil {
				r.mu.Unlock()
				return err
			}
			saved = append(saved, StreamRow{ID: id, Parent: l.dir.id, Raw: rawOf(e, p, id)})
			if descend {
				children = append(children, streamDir{id: id, path: e.Path, depth: l.dir.depth + 1, modified: e.Modified})
			}
			if e.IsDir {
				r.st.Folders++
			} else {
				r.st.Files++
			}
		}
		if l.keep {
			r.failed[l.dir.id] = true
		} else if _, err := done.Exec(codeSkipped, l.dir.id); err != nil {
			r.mu.Unlock()
			return err
		}
		if r.so.Errors != nil {
			*r.so.Errors = append(*r.so.Errors, l.errs...)
		}
		r.st.ScanErrors += uint64(len(l.errs))
	}
	r.mu.Unlock()
	if err := tx.Commit(); err != nil {
		return err
	}
	// Only now (durable) do the folders leave the work list and their
	// children join it; beyond MaxQueue they wait in the index.
	r.mu.Lock()
	for _, l := range batch {
		delete(r.inFlight, l.dir.id)
	}
	for _, c := range children {
		if r.queue.Len() < r.so.MaxQueue {
			r.push(c)
		} else {
			r.more = true
		}
	}
	r.mu.Unlock()
	n := r.saved.Add(uint64(len(saved)))
	if r.so.OnBatch != nil && len(saved) > 0 {
		r.so.OnBatch(saved)
	}
	if r.so.Progress != nil {
		r.so.Progress(n)
	}
	return nil
}

// dirHeap: waiting folders, highest priority first (waiting time added).
type dirHeap []streamDir

func (h dirHeap) Len() int { return len(h) }
func (h dirHeap) Less(i, j int) bool {
	now := time.Now()
	pi := h[i].prio + now.Sub(h[i].queued).Minutes()
	pj := h[j].prio + now.Sub(h[j].queued).Minutes()
	return pi > pj
}
func (h dirHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *dirHeap) Push(x any)   { *h = append(*h, x.(streamDir)) }
func (h *dirHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// unlisted counts the root's folders not read yet.
func (r *streamRun) unlisted() int {
	var n int
	if err := r.db.conn.QueryRow("SELECT COUNT(*) FROM entries WHERE root = ? AND is_dir = 1 AND probe = ?", r.rootID, codeUnlisted).Scan(&n); err != nil {
		return len(r.failed) + 1 // unsure: look again
	}
	return n
}

// HasUnlisted: root's index was cut short by a streaming run (it can go on).
func (d *Db) HasUnlisted(root string) bool {
	var n int
	err := d.conn.QueryRow(`SELECT COUNT(*) FROM entries WHERE is_dir = 1 AND probe = ? AND root = (SELECT id FROM roots WHERE path = ?)`,
		codeUnlisted, d.stored(root)).Scan(&n)
	return err == nil && n > 0
}
