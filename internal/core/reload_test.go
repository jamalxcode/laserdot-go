package core

import (
	"encoding/json"
	"github.com/jamalxcode/laserdot-go/internal/watch"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/index"
)

// A full index builds the in-memory index from what it just wrote; it must
// be exactly what reading the database back gives, with other folders
// already indexed and a folder inside the new one replaced.
func TestIndexKeepsTheSameIndexAsReopening(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	write(t, a, "invoice march.pdf", "x")
	write(t, a, "sub/photo.jpg", "not really a jpeg")
	write(t, b, "notes.txt", "y")
	write(t, b, "inner/clip.mp4", "z")
	if err := os.Symlink("notes.txt", filepath.Join(b, "link")); err != nil {
		t.Fatal(err)
	}
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{a, filepath.Join(b, "inner"), b} {
		if _, err := ld.Index(root); err != nil {
			t.Fatal(err)
		}
	}
	queries := []string{"*", "kind:video", "in:" + b, "march", "type:folder"}
	live := map[string]string{}
	for _, q := range queries {
		live[q] = resultsJSON(t, ld, q)
	}
	ld.Close()
	again, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	again.reload() // from the database, not the snapshot
	for _, q := range queries {
		if got := resultsJSON(t, again, q); got != live[q] {
			t.Errorf("%s:\nlive     %s\nreopened %s", q, live[q], got)
		}
	}
}

func resultsJSON(t *testing.T, ld *Laserdot, q string) string {
	t.Helper()
	r, err := ld.Search(q, SearchOptions{Page: pageAll()})
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	r.ElapsedUS = 0
	data, _ := json.Marshal(r)
	return string(data)
}

func pageAll() index.Page {
	p := index.DefaultPage
	p.Limit = 1000
	return p
}

// A folder that cannot be read is listed, not silently missing.
func TestUnreadableFoldersAreListed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(dir, "home")
	write(t, root, "ok.txt", "x")
	write(t, root, "secret/hidden.txt", "y")
	locked := filepath.Join(root, "secret")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	r, err := ld.Unreadable()
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != 1 || len(r.Items) != 1 || r.Items[0].Path != locked || r.Items[0].Reason != "permission" {
		t.Fatalf("unreadable: %+v", r)
	}
	// Readable again and indexed again: the list empties.
	os.Chmod(locked, 0o755)
	if _, err := ld.Forget(root); err != nil {
		t.Fatal(err)
	}
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	if r, _ := ld.Unreadable(); r.Total != 0 || len(r.Items) != 0 {
		t.Fatalf("after fixing: %+v", r)
	}
}

// Entries an older version indexed under ~/Library (caches, app data) are
// taken out; iCloud Drive stays.
func TestPruneLibraryRemovesCachesKeepsICloud(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(dir, "home")
	write(t, home, "Documents/report.pdf", "x")
	write(t, home, "Library/Caches/com.apple.wallpaper.caches/abc.png", "y")
	write(t, home, "Library/Containers/app/Data/thing.jpg", "z")
	write(t, home, "Library/Mobile Documents/com~apple~CloudDocs/notes.txt", "n")
	// Indexed as an old version did: ~/Library not restricted (HOME elsewhere).
	t.Setenv("HOME", filepath.Join(dir, "elsewhere"))
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	if _, err := ld.Index(home); err != nil {
		t.Fatal(err)
	}
	names := func(q string) []string {
		r, err := ld.Search(q, SearchOptions{Page: pageAll()})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range r.Hits {
			out = append(out, h.Name)
		}
		return out
	}
	if got := names("abc"); len(got) != 1 {
		t.Fatalf("the cache file should be indexed by the old rules: %v", got)
	}
	t.Setenv("HOME", home)
	u, err := ld.PruneLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if u.Removed == 0 {
		t.Fatalf("nothing pruned: %+v", u)
	}
	for _, q := range []string{"abc", "thing", "Caches"} {
		if got := names(q); len(got) != 0 {
			t.Errorf("%s still found: %v", q, got)
		}
	}
	for _, q := range []string{"notes", "report"} {
		if got := names(q); len(got) != 1 {
			t.Errorf("%s should stay: %v", q, got)
		}
	}
	// Nothing left to prune: a no-op.
	if u, _ := ld.PruneLibrary(); u.Removed != 0 {
		t.Errorf("second prune removed %d", u.Removed)
	}
}

// A file deleted while its change was missed (no watcher here) is left out
// of the results at once, and the index forgets it.
func TestDeletedFilesLeaveTheResults(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(dir, "home")
	write(t, root, "pics/beach.jpg", "x")
	write(t, root, "pics/sunset.jpg", "y")
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	ld.WaitBackground()
	if err := os.Remove(filepath.Join(root, "pics/beach.jpg")); err != nil {
		t.Fatal(err)
	}
	r, err := ld.Search("jpg", SearchOptions{Page: pageAll()})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Hits) != 1 || r.Hits[0].Name != "sunset.jpg" || r.Total != 1 {
		t.Fatalf("results: total %d %+v", r.Total, r.Hits)
	}
	ld.WaitBackground()
	// Gone from the index itself: a reopened index does not have it.
	ld.Close()
	again, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	again.reload()
	if r, _ := again.search("beach", SearchOptions{Page: pageAll()}); len(r.Hits) != 0 {
		t.Fatalf("still indexed: %+v", r.Hits)
	}
}

// Kind order: by kind (app, archive, audio, code, document, folder, image…),
// then by name; reversed it is the other way round.
func TestSortByKind(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(dir, "home")
	for _, f := range []string{"b.jpg", "a.png", "notes.pdf", "song.mp3", "clip.mp4", "z/inside.txt"} {
		write(t, root, f, "x")
	}
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	page := pageAll()
	page.Sort = index.ByKind
	r, err := ld.Search("in:"+root+" -inside", SearchOptions{Page: page})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range r.Hits {
		got = append(got, h.Name)
	}
	// audio, document, folder (z), image (a, b), video
	want := []string{"song.mp3", "notes.pdf", "z", "a.png", "b.jpg", "clip.mp4"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("kind order: %v", got)
	}
	page.Reverse = true
	r, _ = ld.Search("in:"+root+" -inside", SearchOptions{Page: page})
	if len(r.Hits) == 0 || r.Hits[0].Name != "clip.mp4" {
		t.Errorf("reversed: %+v", r.Hits)
	}
}

// A change the watcher never delivers is found by the catch-up, which then
// restarts watching.
func TestCatchUpFindsMissedChanges(t *testing.T) {
	old := CatchUpEvery
	CatchUpEvery = 600 * time.Millisecond
	defer func() { CatchUpEvery = old }()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(dir, "home")
	write(t, root, "keep.txt", "x")
	write(t, root, "lost.txt", "y")
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 4)
	var missed atomic.Int64
	// Polling once an hour: no change arrives as an event during the test.
	w := ld.Watch(watch.Options{ForcePoll: true, PollInterval: time.Hour, Latency: 50 * time.Millisecond}, WatchEvents{
		Started: func(watch.Backend) { started <- struct{}{} },
		Missed:  func(u UpdateStats) { missed.Add(int64(u.Removed)) },
	})
	defer w.Stop()
	<-started
	time.Sleep(200 * time.Millisecond) // after the refresh that starts watching
	os.Remove(filepath.Join(root, "lost.txt"))
	deadline := time.Now().Add(5 * time.Second)
	for w.Health().Missed == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	h := w.Health()
	if h.Missed == 0 || h.Restarts == 0 || h.LastCatchUp == 0 || missed.Load() != 1 {
		t.Fatalf("health %+v, missed %d", h, missed.Load())
	}
	if r, _ := ld.search("lost", SearchOptions{Page: pageAll()}); len(r.Hits) != 0 {
		t.Fatalf("still indexed: %+v", r.Hits)
	}
}

// Results carry when they were last opened from Laserdot, and can be
// ordered by it: the most recently opened first, never-opened ones after.
func TestLastOpenedFieldAndOrder(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(dir, "home")
	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		write(t, root, f, "x")
	}
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	if _, err := ld.Index(root); err != nil {
		t.Fatal(err)
	}
	if err := ld.RecordOpen(filepath.Join(root, "b.txt")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // seconds resolution
	if err := ld.RecordOpen(filepath.Join(root, "c.txt")); err != nil {
		t.Fatal(err)
	}
	page := pageAll()
	page.Sort = index.ByOpened
	r, err := ld.Search("txt", SearchOptions{Page: page})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range r.Hits {
		got = append(got, h.Name)
	}
	if strings.Join(got, ",") != "c.txt,b.txt,a.txt" {
		t.Errorf("order: %v", got)
	}
	if r.Hits[0].Opened == 0 || r.Hits[2].Opened != 0 {
		t.Errorf("opened: %+v", r.Hits)
	}
}
