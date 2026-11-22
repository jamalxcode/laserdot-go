package core

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"testing"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/index"
)

// bigStore: n synthetic entries in folders of ~50 (names, kinds, sizes, dates
// like a real disk), as this Mac's index.
func bigStore(t testing.TB, n int) *Laserdot {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	ld, err := Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ld.Close() })
	r := rand.New(rand.NewPCG(3, 4))
	words := []string{"invoice", "report", "holiday", "march", "final", "draft", "IMG", "scan", "photo", "movie"}
	exts := []string{"pdf", "txt", "jpg", "png", "mkv", "mp4", "flac", "docx", "heic", "mov"}
	raw := []index.RawEntry{{Parent: -1, Name: "/bench", IsDir: true}}
	dirs := []int{0}
	for len(raw) < n {
		if len(raw)%50 == 0 {
			raw = append(raw, index.RawEntry{Parent: dirs[r.IntN(len(dirs))], Name: fmt.Sprintf("%s-%d", words[r.IntN(len(words))], len(raw)), IsDir: true})
			dirs = append(dirs, len(raw)-1)
			continue
		}
		raw = append(raw, index.RawEntry{Parent: dirs[len(dirs)-1-r.IntN(min(len(dirs), 20))],
			Name: fmt.Sprintf("%s %s %d.%s", words[r.IntN(len(words))], words[r.IntN(len(words))], r.IntN(10000), exts[r.IntN(len(exts))]),
			Size: uint64(r.IntN(1 << 30)), Modified: time.Now().Unix() - int64(r.IntN(200_000_000))})
	}
	s := index.Build(raw)
	ld.mu.Lock()
	ld.store = s
	ld.mu.Unlock()
	return ld
}

// The choices for a huge search come back within their time budget (with
// what was counted by then), and a choice in the query is always offered.
func TestChoicesStayWithinTheirBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a 200,000-entry index")
	}
	ld := bigStore(t, 200_000)
	saved := RefineBudget
	defer func() { RefineBudget = saved }()
	start := time.Now()
	full, err := ld.Refinements("kind:image")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("choices for kind:image over 200,000 entries: %v, %d groups", time.Since(start), len(full))
	RefineBudget = time.Nanosecond // no time at all
	start = time.Now()
	quick, err := ld.Refinements("photo ext:jpg")
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("out of time, the choices still took %v", d)
	}
	found := false
	for _, g := range quick {
		for _, o := range g.Options {
			if o.Token == "ext:jpg" && o.Selected {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("the chosen ext:jpg stays offered, to remove it: %+v", quick)
	}
}

// BenchmarkRefinements1M: the choices for wide searches over a million entries.
func BenchmarkRefinements1M(b *testing.B) {
	ld := bigStore(b, 1_000_000)
	RefineBudget = time.Hour // measure the full work
	for _, q := range []string{"kind:image", "photo", "kind:video march"} {
		b.Run(q, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := ld.Refinements(q); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
