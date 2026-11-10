package watch

// FSEvents backend (macOS): one stream over all native roots, scheduled on a
// run loop that a dedicated OS thread runs. CoreServices and CoreFoundation
// are bound at run time with purego, so no cgo is needed.

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// <CoreServices/FSEvents.h>
const (
	eventIDSinceNow = ^uint64(0)

	createFlagNoDefer    = 0x02
	createFlagWatchRoot  = 0x04
	createFlagFileEvents = 0x10

	flagMustScanSubDirs = 0x01
	flagUserDropped     = 0x02
	flagKernelDropped   = 0x04
	flagEventIDsWrapped = 0x08
	flagHistoryDone     = 0x10
	flagRootChanged     = 0x20
	// Item flags (file-level events): any of them means the path names one
	// item, whose folder is what changed.
	flagItemMask = 0x7FFF00

	cfStringEncodingUTF8 = 0x08000100
	cfRunLoopRunFinished = 1
)

// rescanFlags mean "events were lost or coalesced here: rescan everything".
const rescanFlags = flagMustScanSubDirs | flagUserDropped | flagKernelDropped | flagRootChanged | flagEventIDsWrapped

type fsEventStreamContext struct {
	version         int
	info            uintptr
	retain          uintptr
	release         uintptr
	copyDescription uintptr
}

var (
	cfStringCreateWithCString func(alloc uintptr, s string, enc uint32) uintptr
	cfArrayCreate             func(alloc uintptr, values *uintptr, n int, callbacks uintptr) uintptr
	cfRelease                 func(cf uintptr)
	cfRunLoopGetCurrent       func() uintptr
	cfRunLoopRunInMode        func(mode uintptr, seconds float64, returnAfterSourceHandled uint8) int32
	cfRunLoopStop             func(rl uintptr)
	fsEventStreamCreate       func(alloc uintptr, cb uintptr, ctx *fsEventStreamContext, paths uintptr, since uint64, latency float64, flags uint32) uintptr
	fsEventStreamSchedule     func(stream, rl, mode uintptr)
	fsEventStreamStart        func(stream uintptr) uint8
	fsEventStreamStop         func(stream uintptr)
	fsEventStreamInvalidate   func(stream uintptr)
	fsEventStreamRelease      func(stream uintptr)
	fsEventsGetCurrentEventID func() uint64

	cfTypeArrayCallBacks uintptr // &kCFTypeArrayCallBacks
	cfRunLoopDefaultMode uintptr // kCFRunLoopDefaultMode
	fsEventsCallbackPtr  uintptr
	loadOnce             sync.Once
	loadErr              error
)

func bind(lib uintptr, fptr any, name string) error {
	sym, err := purego.Dlsym(lib, name)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	purego.RegisterFunc(fptr, sym)
	return nil
}

func load() error {
	loadOnce.Do(func() {
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		cs, err := purego.Dlopen("/System/Library/Frameworks/CoreServices.framework/CoreServices", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		for _, b := range []struct {
			lib  uintptr
			fptr any
			name string
		}{
			{cf, &cfStringCreateWithCString, "CFStringCreateWithCString"},
			{cf, &cfArrayCreate, "CFArrayCreate"},
			{cf, &cfRelease, "CFRelease"},
			{cf, &cfRunLoopGetCurrent, "CFRunLoopGetCurrent"},
			{cf, &cfRunLoopRunInMode, "CFRunLoopRunInMode"},
			{cf, &cfRunLoopStop, "CFRunLoopStop"},
			{cs, &fsEventStreamCreate, "FSEventStreamCreate"},
			{cs, &fsEventStreamSchedule, "FSEventStreamScheduleWithRunLoop"},
			{cs, &fsEventStreamStart, "FSEventStreamStart"},
			{cs, &fsEventStreamStop, "FSEventStreamStop"},
			{cs, &fsEventStreamInvalidate, "FSEventStreamInvalidate"},
			{cs, &fsEventStreamRelease, "FSEventStreamRelease"},
			{cs, &fsEventsGetCurrentEventID, "FSEventsGetCurrentEventId"},
		} {
			if loadErr = bind(b.lib, b.fptr, b.name); loadErr != nil {
				return
			}
		}
		if cfTypeArrayCallBacks, loadErr = purego.Dlsym(cf, "kCFTypeArrayCallBacks"); loadErr != nil {
			return
		}
		mode, err := purego.Dlsym(cf, "kCFRunLoopDefaultMode")
		if err != nil {
			loadErr = err
			return
		}
		// mode is the address of a CFStringRef variable: read it.
		cfRunLoopDefaultMode = **(**uintptr)(unsafe.Pointer(&mode))
		fsEventsCallbackPtr = purego.NewCallback(fsEventsCallback)
	})
	return loadErr
}

func currentEventID() (uint64, bool) {
	if load() != nil {
		return 0, false
	}
	return fsEventsGetCurrentEventID(), true
}

// streams maps a stream's context info to its state, for the callback.
var (
	streamsMu  sync.Mutex
	streams    = map[uintptr]*fsStream{}
	nextStream uintptr
)

type fsStream struct {
	q       *queue
	roots   map[string]bool
	stopped atomic.Bool
	runLoop uintptr
	done    chan struct{}
}

func startNative(roots []WatchRoot, opts Options, _ scan.Options, q *queue) (native, Backend, bool, error) {
	if err := load(); err != nil {
		return nil, Poll, false, err
	}
	since := eventIDSinceNow
	replays := true
	for _, r := range roots {
		if r.Since == 0 {
			replays = false
			break
		}
		since = min(since, r.Since)
	}
	if !replays {
		since = eventIDSinceNow
	}
	s := &fsStream{q: q, roots: map[string]bool{}, done: make(chan struct{})}
	paths := make([]string, len(roots))
	for i, r := range roots {
		paths[i] = r.Path
		s.roots[r.Path] = true
	}
	streamsMu.Lock()
	nextStream++
	id := nextStream
	streams[id] = s
	streamsMu.Unlock()

	ready := make(chan error, 1)
	go s.run(id, paths, since, opts.Latency.Seconds(), ready)
	if err := <-ready; err != nil {
		streamsMu.Lock()
		delete(streams, id)
		streamsMu.Unlock()
		return nil, Poll, false, err
	}
	return s, FSEvents, replays, nil
}

// run owns the stream: it is created, scheduled, run and released on one
// locked OS thread.
func (s *fsStream) run(id uintptr, paths []string, since uint64, latency float64, ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(s.done)
	refs := make([]uintptr, 0, len(paths))
	for _, p := range paths {
		if ref := cfStringCreateWithCString(0, p, cfStringEncodingUTF8); ref != 0 {
			refs = append(refs, ref)
		}
	}
	var array uintptr
	if len(refs) > 0 {
		array = cfArrayCreate(0, &refs[0], len(refs), cfTypeArrayCallBacks)
	}
	for _, ref := range refs {
		cfRelease(ref) // the array retains them
	}
	if array == 0 {
		ready <- errors.New("FSEvents: cannot build the path list")
		return
	}
	ctx := fsEventStreamContext{info: id}
	stream := fsEventStreamCreate(0, fsEventsCallbackPtr, &ctx, array, since, latency,
		createFlagNoDefer|createFlagWatchRoot|createFlagFileEvents)
	cfRelease(array)
	if stream == 0 {
		ready <- errors.New("FSEventStreamCreate failed")
		return
	}
	s.runLoop = cfRunLoopGetCurrent()
	fsEventStreamSchedule(stream, s.runLoop, cfRunLoopDefaultMode)
	if fsEventStreamStart(stream) == 0 {
		fsEventStreamInvalidate(stream)
		fsEventStreamRelease(stream)
		ready <- errors.New("FSEventStreamStart failed")
		return
	}
	ready <- nil
	// Run in slices so a stop that lands before the loop runs is noticed.
	for !s.stopped.Load() {
		if cfRunLoopRunInMode(cfRunLoopDefaultMode, 0.25, 0) == cfRunLoopRunFinished {
			break
		}
	}
	fsEventStreamStop(stream)
	fsEventStreamInvalidate(stream)
	fsEventStreamRelease(stream)
	streamsMu.Lock()
	delete(streams, id)
	streamsMu.Unlock()
}

func (s *fsStream) stop() {
	s.stopped.Store(true)
	cfRunLoopStop(s.runLoop)
	<-s.done
}

func goString(p unsafe.Pointer) string {
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}

// fsEventsCallback is the FSEventStreamCallback: paths is a char** (no
// UseCFTypes flag); flags and ids are arrays of n.
func fsEventsCallback(_ uintptr, info uintptr, n uintptr, paths, flags, ids unsafe.Pointer) {
	streamsMu.Lock()
	s := streams[info]
	streamsMu.Unlock()
	if s == nil || n == 0 {
		return
	}
	ps := unsafe.Slice((*unsafe.Pointer)(paths), n)
	fs := unsafe.Slice((*uint32)(flags), n)
	is := unsafe.Slice((*uint64)(ids), n)
	for i := range ps {
		s.handle(goString(ps[i]), fs[i], is[i])
	}
}

func (s *fsStream) handle(path string, flag uint32, id uint64) {
	if flag&flagHistoryDone != 0 {
		s.q.push(event{kind: evHistoryDone})
		return
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	switch {
	case flag&rescanFlags != 0:
		s.q.changed(path, true)
	case flag&flagItemMask != 0 && !s.roots[path]:
		// A file-level event names the item: its folder changed.
		s.q.changed(filepath.Dir(path), false)
	default:
		s.q.changed(path, false)
	}
	s.q.push(event{kind: evID, id: id})
}
