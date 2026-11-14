package crash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBugsAreCaughtRecordedAndLoopsComeBack(t *testing.T) {
	log := filepath.Join(t.TempDir(), "crash.log")
	SetLog(log)
	defer SetLog("")
	func() {
		defer Guard("a request")
		var m map[string]int
		m["x"] = 1 // panics: assignment to a nil map
	}()
	runs := 0
	Keep("the watcher", time.Millisecond, func() {
		runs++
		if runs < 3 {
			panic("boom")
		}
	})
	done := make(chan struct{})
	Go("a background task", func() { defer close(done); panic("background") })
	<-done
	time.Sleep(20 * time.Millisecond)
	b, _ := os.ReadFile(log)
	text := string(b)
	for _, want := range []string{"a request", "the watcher", "a background task", "nil map"} {
		if !strings.Contains(text, want) {
			t.Errorf("log lacks %q:\n%s", want, text)
		}
	}
	if runs != 3 || Count() < 4 {
		t.Errorf("runs %d, caught %d", runs, Count())
	}
}
