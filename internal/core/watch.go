package core

import (
	"sync/atomic"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/watch"
)

func init() { currentEventID = watch.CurrentEventID }

// WatchEvents reports what the watch loop did (for logs).
type WatchEvents struct {
	// Applied is called after each applied batch.
	Applied func(UpdateStats)
	// Error is called when a batch fails (the loop keeps going).
	Error func(error)
	// Started is called with the backend each time watching (re)starts.
	Started func(watch.Backend)
}

// Watcher keeps an index current until stopped.
type Watcher struct {
	ld      *Laserdot
	opts    watch.Options
	stop    chan struct{}
	done    chan struct{}
	restart atomic.Bool
	backend atomic.Value // string
}

// Watch starts keeping the index current with the platform's file events
// (FSEvents, inotify, or polling for network volumes). Without event
// replay, every root is refreshed first to catch up on changes made while
// not watching.
func (ld *Laserdot) Watch(opts watch.Options, ev WatchEvents) *Watcher {
	w := &Watcher{ld: ld, opts: opts, stop: make(chan struct{}), done: make(chan struct{})}
	w.backend.Store("None")
	go w.run(ev)
	return w
}

// Backend names the change detector in use ("None" before it starts).
func (w *Watcher) Backend() string { return w.backend.Load().(string) }

// RootsChanged makes the watcher pick up added or removed roots.
func (w *Watcher) RootsChanged() { w.restart.Store(true) }

// Stop stops watching and waits for the loop to end.
func (w *Watcher) Stop() {
	select {
	case <-w.stop:
	default:
		close(w.stop)
	}
	<-w.done
}

func (w *Watcher) stopped() bool {
	select {
	case <-w.stop:
		return true
	default:
		return false
	}
}

func (w *Watcher) run(ev WatchEvents) {
	defer close(w.done)
	// Older versions indexed all of ~/Library; take out what the current
	// rules leave out (caches, app data) before following changes.
	if _, err := w.ld.PruneLibrary(); err != nil && ev.Error != nil {
		ev.Error(err)
	}
	report := func(u UpdateStats, err error) {
		if err != nil {
			if ev.Error != nil {
				ev.Error(err)
			}
		} else if ev.Applied != nil && (u.Added+u.Removed+u.Renamed+u.Modified+u.Probed > 0 || len(u.OfflineRoots) > 0) {
			ev.Applied(u)
		}
	}
	for !w.stopped() {
		w.restart.Store(false)
		roots, err := w.ld.Roots()
		if err != nil {
			report(UpdateStats{}, err)
			w.sleep(5 * time.Second)
			continue
		}
		var wr []watch.WatchRoot
		for _, r := range roots {
			var since uint64
			if r.LastEventID != nil {
				since = *r.LastEventID
			}
			wr = append(wr, watch.WatchRoot{Path: r.Path, Since: since})
		}
		// Attached drives are watched too; their changes go to their index.
		w.ld.mu.RLock()
		for _, d := range w.ld.drives {
			wr = append(wr, watch.WatchRoot{Path: d.mount})
		}
		w.ld.mu.RUnlock()
		if len(wr) == 0 {
			w.backend.Store("None")
			for !w.stopped() && !w.restart.Load() {
				w.sleep(500 * time.Millisecond)
			}
			continue
		}
		wt, err := watch.New(wr, w.opts, w.ld.ScanOptions())
		if err != nil {
			report(UpdateStats{}, err)
			w.sleep(5 * time.Second)
			continue
		}
		w.backend.Store(wt.Backend().String())
		if ev.Started != nil {
			ev.Started(wt.Backend())
		}
		if !wt.Replays() {
			report(w.ld.Refresh())
		}
		lastPending := time.Now()
		for !w.stopped() && !w.restart.Load() {
			b, ok := wt.NextBatch(500 * time.Millisecond)
			if ok && len(b.Changes) > 0 {
				report(w.ld.ApplyChanges(b.Changes))
			}
			if ok && b.EventID != 0 {
				for _, r := range roots {
					if err := w.ld.SetEventID(r.Path, b.EventID); err != nil {
						report(UpdateStats{}, err)
					}
				}
			}
			// Files that were still being written get probed once stable.
			if time.Since(lastPending) > 5*time.Second {
				lastPending = time.Now()
				report(w.ld.CheckPending())
			}
		}
		wt.Close()
	}
}

func (w *Watcher) sleep(d time.Duration) {
	select {
	case <-w.stop:
	case <-time.After(d):
	}
}
