package core

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The crash test: a real laserdot process indexes, refreshes and looks for
// duplicates, and is killed (kill -9: no clean shutdown, as in a crash or a
// power cut) at a random moment. Every time, the index must open again, go
// on, and end up exactly as a fresh index of the same files. Rounds:
// LASERDOT_CRASH_ROUNDS (default 6).
func TestKilledAtAnyMomentTheIndexRecovers(t *testing.T) {
	if os.Getenv("LASERDOT_CRASH_CHILD") != "" {
		crashChild()
		return
	}
	if testing.Short() {
		t.Skip("spawns processes")
	}
	rounds := 6
	if n, err := strconv.Atoi(os.Getenv("LASERDOT_CRASH_ROUNDS")); err == nil && n > 0 {
		rounds = n
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(dir, "home")
	db := filepath.Join(dir, "data", "index.db")
	rng := rand.New(rand.NewPCG(7, uint64(rounds)))
	// Enough files that each step takes a moment to be caught in.
	for i := 0; i < 3000; i++ {
		write(t, root, fmt.Sprintf("d%02d/sub%d/file %04d.txt", i%40, i%7, i), fmt.Sprintf("content %d", i%500))
	}
	for round := 0; round < rounds; round++ {
		// The files change between crashes.
		for k := 0; k < 50; k++ {
			p := filepath.Join(root, fmt.Sprintf("d%02d/new %d-%d.txt", rng.IntN(40), round, k))
			write(t, filepath.Dir(p), filepath.Base(p), "new")
		}
		victims, _ := filepath.Glob(filepath.Join(root, fmt.Sprintf("d%02d/*/*.txt", rng.IntN(40))))
		for _, v := range victims[:len(victims)/2] {
			os.Remove(v)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestKilledAtAnyMomentTheIndexRecovers$")
		cmd.Env = append(os.Environ(), "LASERDOT_CRASH_CHILD=1", "LASERDOT_CRASH_DB="+db, "LASERDOT_CRASH_ROOT="+root)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(30+rng.IntN(600)) * time.Millisecond)
		cmd.Process.Kill()
		cmd.Wait()

		// It opens, and carries on from where it was killed.
		ld, err := Open(db)
		if err != nil {
			t.Fatalf("round %d: the index does not open after a crash: %v", round, err)
		}
		if _, err := ld.Index(root); err != nil {
			t.Fatalf("round %d: indexing after a crash failed: %v", round, err)
		}
		if _, err := ld.FindDuplicates(exactOnly()); err != nil {
			t.Fatalf("round %d: looking for duplicates after a crash failed: %v", round, err)
		}
		got := ld.Stats().Entries
		ld.Close()

		fresh, err := Open(filepath.Join(dir, fmt.Sprintf("fresh%d", round), "index.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fresh.Index(root); err != nil {
			t.Fatal(err)
		}
		want := fresh.Stats().Entries
		fresh.Close()
		if got != want {
			t.Fatalf("round %d: after the crash the index holds %d entries, a fresh one %d", round, got, want)
		}
	}
}

// crashChild is the process that gets killed: it keeps working until then.
func crashChild() {
	db, root := os.Getenv("LASERDOT_CRASH_DB"), os.Getenv("LASERDOT_CRASH_ROOT")
	ld, err := Open(db)
	if err != nil {
		os.Exit(2)
	}
	for {
		ld.Index(root)
		ld.Refresh()
		ld.FindDuplicates(exactOnly())
		ld.QuickCheck()
	}
}

// A folder that disappears while it is being indexed (a drive pulled out)
// ends indexing with an answer, not a hang, and the index stays usable.
func TestAFolderPulledAwayMidIndexIsSurvived(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(dir, "drive")
	for i := 0; i < 4000; i++ {
		write(t, root, fmt.Sprintf("f%02d/g%d/%04d.txt", i%50, i%9, i), "x")
	}
	ld, err := Open(filepath.Join(dir, "data", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	done := make(chan error, 1)
	go func() { _, err := ld.Index(root); done <- err }()
	time.Sleep(5 * time.Millisecond)
	os.RemoveAll(root)
	select {
	case <-done: // finished or reported an error: either is fine
	case <-time.After(30 * time.Second):
		t.Fatal("indexing hung when its folder vanished")
	}
	if _, err := ld.Refresh(); err != nil {
		t.Fatalf("the index is not usable afterwards: %v", err)
	}
	if r, err := ld.search("txt", SearchOptions{Page: pageAll()}); err != nil {
		t.Fatalf("search failed afterwards: %v (%d hits)", err, len(r.Hits))
	}
}

// A first index that never finished (the folder is known, its files are
// not) is redone when the daemon starts (Refresh), with nothing asked.
func TestAnUnfinishedFirstIndexHealsByItself(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(dir, "home")
	for i := 0; i < 20; i++ {
		write(t, root, fmt.Sprintf("f%d.txt", i), "x")
	}
	db := filepath.Join(dir, "data", "index.db")
	ld, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	// As if killed right after the folder was registered.
	if _, _, err := ld.db.BeginRoot(root); err != nil {
		t.Fatal(err)
	}
	ld.Close()
	ld, err = Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer ld.Close()
	if _, err := ld.Refresh(); err != nil {
		t.Fatal(err)
	}
	fresh, err := Open(filepath.Join(dir, "fresh", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if _, err := fresh.Index(root); err != nil {
		t.Fatal(err)
	}
	if n, want := ld.Stats().Entries, fresh.Stats().Entries; n != want || n == 0 {
		t.Fatalf("after starting again the index holds %d entries, a fresh one %d", n, want)
	}
}
