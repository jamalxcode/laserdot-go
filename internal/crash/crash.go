// Package crash keeps laserdotd running through a bug, and keeps a record of
// it on this Mac. A panic in one request or one background task is caught,
// written to a local log file (never sent anywhere: Save Diagnostics in the
// app includes it), and the rest of the daemon carries on.
package crash

import (
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"
)

// MaxLog: the log keeps its newest part below this size.
const MaxLog = 1 << 20

var (
	mu      sync.Mutex
	logPath string
	count   int
)

// SetLog names the log file (e.g. beside the index). Fatal runtime errors
// that cannot be caught (out of memory, a broken map) are written there too.
func SetLog(path string) {
	mu.Lock()
	logPath = path
	mu.Unlock()
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		_ = debug.SetCrashOutput(f, debug.CrashOptions{})
		f.Close() // SetCrashOutput keeps its own copy of the file
	}
}

// Count: how many panics were caught since the daemon started.
func Count() int {
	mu.Lock()
	defer mu.Unlock()
	return count
}

// Record writes one caught panic to the log.
func Record(what string, v any, stack []byte) {
	mu.Lock()
	defer mu.Unlock()
	count++
	if logPath == "" {
		fmt.Fprintf(os.Stderr, "laserdotd: caught a bug in %s: %v\n%s\n", what, v, stack)
		return
	}
	trim(logPath)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "=== %s: caught a bug in %s: %v\n%s\n", time.Now().Format(time.RFC3339), what, v, stack)
}

// trim keeps the newest half when the log has grown past MaxLog.
func trim(path string) {
	b, err := os.ReadFile(path)
	if err != nil || len(b) < MaxLog {
		return
	}
	_ = os.WriteFile(path, b[len(b)-MaxLog/2:], 0o600)
}

// Guard, deferred directly (`defer crash.Guard("probe")`), catches a panic in
// the function and records it; the function then simply returns.
func Guard(what string) {
	if v := recover(); v != nil {
		Record(what, v, debug.Stack())
	}
}

// Go runs fn in a goroutine whose panic is caught and recorded.
func Go(what string, fn func()) {
	go func() {
		defer Guard(what)
		fn()
	}()
}

// Keep runs fn, and runs it again after pause each time it panics (a
// long-lived loop such as the file watcher must come back). It returns when
// fn returns normally.
func Keep(what string, pause time.Duration, fn func()) {
	for {
		if !panics(what, fn) {
			return
		}
		time.Sleep(pause)
	}
}

func panics(what string, fn func()) (panicked bool) {
	defer func() {
		if v := recover(); v != nil {
			Record(what, v, debug.Stack())
			panicked = true
		}
	}()
	fn()
	return false
}
