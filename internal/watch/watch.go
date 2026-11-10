// Package watch is file watching (spec §2.1.2, §2.6.6). Every backend reports
// the same thing — "something under this folder changed" (scan.Change) — and
// the index update engine rescans and diffs; no backend is trusted for
// details.
//
//   - macOS: FSEvents, with event ids. The last applied id is persisted, so
//     changes made while the app was not running are replayed on start
//     (sinceWhen), and dropped-event flags become recursive rescans. Bound
//     without cgo through purego.
//   - Linux: inotify, one watch per folder; queue overflow becomes a
//     recursive rescan. No replay: callers reconcile with a full refresh on
//     start.
//   - Polling for network volumes (SMB/NFS/AFP/WebDAV), where change events
//     are unreliable, or when forced: folder listings are compared between
//     polls.
package watch

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// WatchRoot is a folder to watch, with the FSEvents event id to replay from.
type WatchRoot struct {
	Path string
	// Since is the last applied FSEvents event id; 0 means none (no replay).
	Since uint64
}

// Options control a Watcher.
type Options struct {
	// Latency: events are coalesced for this long before a batch is
	// delivered.
	Latency time.Duration
	// PollInterval is the rescan interval for polled roots.
	PollInterval time.Duration
	// ForcePoll polls every root instead of using native events.
	ForcePoll bool
}

// DefaultOptions are the spec's defaults: 300 ms coalescing, 60 s polls.
func DefaultOptions() Options {
	return Options{Latency: 300 * time.Millisecond, PollInterval: 60 * time.Second}
}

// Backend is the mechanism delivering events.
type Backend int

const (
	Poll Backend = iota
	FSEvents
	Inotify
)

func (b Backend) String() string {
	switch b {
	case FSEvents:
		return "FsEvents"
	case Inotify:
		return "Inotify"
	default:
		return "Poll"
	}
}

// Batch is a set of coalesced changes, ready for the update engine.
type Batch struct {
	Changes []scan.Change
	// EventID is the highest FSEvents event id in the batch (0: none).
	// Persist it after applying the batch.
	EventID uint64
	// HistoryDone: FSEvents finished replaying history since the stored id.
	HistoryDone bool
}

type eventKind uint8

const (
	evChanged eventKind = iota
	evID
	evHistoryDone
)

type event struct {
	kind   eventKind
	change scan.Change
	id     uint64
}

// maxQueued bounds the event queue: beyond it, queued events are replaced
// by a recursive change of every root (a full rescan loses nothing).
const maxQueued = 1 << 20

// queue is an unbounded-in-practice FIFO: producers (OS callbacks) never
// block on a slow consumer.
type queue struct {
	mu     sync.Mutex
	items  []event
	head   int
	roots  []string
	signal chan struct{}
}

func newQueue(roots []string) *queue {
	return &queue{roots: roots, signal: make(chan struct{}, 1)}
}

func (q *queue) push(e event) {
	q.mu.Lock()
	if len(q.items)-q.head >= maxQueued {
		q.items, q.head = q.items[:0], 0
		for _, r := range q.roots {
			q.items = append(q.items, event{kind: evChanged, change: scan.Change{Path: r, Recursive: true}})
		}
	}
	q.items = append(q.items, e)
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

func (q *queue) changed(path string, recursive bool) {
	q.push(event{kind: evChanged, change: scan.Change{Path: path, Recursive: recursive}})
}

// pop waits until deadline for an event.
func (q *queue) pop(deadline time.Time, done <-chan struct{}) (event, bool) {
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		q.mu.Lock()
		if q.head < len(q.items) {
			e := q.items[q.head]
			q.items[q.head] = event{}
			q.head++
			if q.head == len(q.items) {
				q.items, q.head = q.items[:0], 0
			}
			q.mu.Unlock()
			return e, true
		}
		q.mu.Unlock()
		left := time.Until(deadline)
		if left <= 0 {
			return event{}, false
		}
		if timer == nil {
			timer = time.NewTimer(left)
		} else {
			timer.Reset(left)
		}
		select {
		case <-q.signal:
		case <-timer.C:
		case <-done:
			return event{}, false
		}
	}
}

// native is a running native backend.
type native interface {
	stop()
}

// Watcher delivers coalesced changes under a set of roots.
type Watcher struct {
	q       *queue
	backend Backend
	replays bool
	latency time.Duration
	done    chan struct{}
	once    sync.Once
	wg      sync.WaitGroup
	native  native
}

// New starts watching roots. Network volumes (and every root with
// ForcePoll) are polled; the others use the native backend, falling back
// to polling when it cannot start.
func New(roots []WatchRoot, opts Options, scanOpts scan.Options) (*Watcher, error) {
	paths := make([]string, len(roots))
	for i, r := range roots {
		paths[i] = filepath.Clean(r.Path)
	}
	w := &Watcher{
		q:       newQueue(paths),
		backend: Poll,
		latency: opts.Latency,
		done:    make(chan struct{}),
	}
	var poll []string
	var nat []WatchRoot
	for i, r := range roots {
		if opts.ForcePoll || scan.IsNetworkVolume(r.Path) {
			poll = append(poll, paths[i])
		} else {
			nat = append(nat, WatchRoot{Path: paths[i], Since: r.Since})
		}
	}
	if len(nat) > 0 {
		n, backend, replays, err := startNative(nat, opts, scanOpts, w.q)
		if err != nil {
			// No native events available: poll these roots too.
			for _, r := range nat {
				poll = append(poll, r.Path)
			}
		} else {
			w.native, w.backend, w.replays = n, backend, replays
		}
	}
	if len(poll) > 0 {
		interval := opts.PollInterval
		if interval <= 0 {
			interval = DefaultOptions().PollInterval
		}
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			pollLoop(poll, interval, scanOpts, w.q, w.done)
		}()
	}
	return w, nil
}

// Backend reports the native backend in use (Poll when there is none).
func (w *Watcher) Backend() Backend { return w.backend }

// Replays reports whether changes made while not watching are replayed
// (FSEvents with stored ids for every native root). If not, callers should
// run a full refresh on start.
func (w *Watcher) Replays() bool { return w.replays }

// NextBatch waits up to timeout for a change, then gathers everything that
// arrives within the coalescing window. Changes to the same folder are
// merged, and a recursive change makes changes beneath it redundant. False
// if nothing happened (or the watcher is closed).
func (w *Watcher) NextBatch(timeout time.Duration) (Batch, bool) {
	first, ok := w.q.pop(time.Now().Add(timeout), w.done)
	if !ok {
		return Batch{}, false
	}
	var b Batch
	at := map[string]int{}
	add := func(e event) {
		switch e.kind {
		case evChanged:
			if i, ok := at[e.change.Path]; ok {
				b.Changes[i].Recursive = b.Changes[i].Recursive || e.change.Recursive
			} else {
				at[e.change.Path] = len(b.Changes)
				b.Changes = append(b.Changes, e.change)
			}
		case evID:
			b.EventID = max(b.EventID, e.id)
		case evHistoryDone:
			b.HistoryDone = true
		}
	}
	add(first)
	deadline := time.Now().Add(w.latency)
	for {
		e, ok := w.q.pop(deadline, w.done)
		if !ok {
			break
		}
		add(e)
	}
	var recursive []string
	for _, c := range b.Changes {
		if c.Recursive {
			recursive = append(recursive, c.Path)
		}
	}
	if len(recursive) > 0 {
		kept := b.Changes[:0]
		for _, c := range b.Changes {
			if c.Recursive || !underAny(c.Path, recursive) {
				kept = append(kept, c)
			}
		}
		b.Changes = kept
	}
	return b, true
}

// underAny: p is strictly below one of roots.
func underAny(p string, roots []string) bool {
	for _, r := range roots {
		if p != r && isUnder(p, r) {
			return true
		}
	}
	return false
}

// isUnder: p is r or below it, by whole path components.
func isUnder(p, r string) bool {
	switch {
	case p == r:
		return true
	case r == "/":
		return strings.HasPrefix(p, "/")
	default:
		return len(p) > len(r) && p[len(r)] == '/' && strings.HasPrefix(p, r)
	}
}

// Close stops the watcher and its backends. It is safe to call twice.
func (w *Watcher) Close() error {
	w.once.Do(func() {
		close(w.done)
		if w.native != nil {
			w.native.stop()
		}
		w.wg.Wait()
	})
	return nil
}

// CurrentEventID is the current FSEvents event id (macOS): store it when a
// folder is first indexed, so a later start replays everything since. False
// where there are no event ids.
func CurrentEventID() (uint64, bool) { return currentEventID() }
