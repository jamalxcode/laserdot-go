package core

import (
	"testing"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/watch"
)

// With FSEvents and nothing missed for a while, the folder check runs every
// 30 minutes; after a miss, and with other backends, every 5.
func TestQuickCheckIsCalmWhileFSEventsMissesNothing(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	never := time.Time{}
	if got := quickInterval(watch.FSEvents, never, now); got != QuickCheckCalm {
		t.Errorf("FSEvents, nothing missed: %v", got)
	}
	if got := quickInterval(watch.FSEvents, now.Add(-10*time.Minute), now); got != QuickCheckEvery {
		t.Errorf("FSEvents, just missed something: %v", got)
	}
	if got := quickInterval(watch.FSEvents, now.Add(-3*time.Hour), now); got != QuickCheckCalm {
		t.Errorf("FSEvents, calm again after a while: %v", got)
	}
	for _, b := range []watch.Backend{watch.Inotify, watch.Poll} {
		if got := quickInterval(b, never, now); got != QuickCheckEvery {
			t.Errorf("%v: %v", b, got)
		}
	}
}
