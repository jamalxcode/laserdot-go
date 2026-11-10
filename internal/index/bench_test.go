package index

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/query"
)

// synthetic builds n entries in folders of ~50, with realistic names.
func synthetic(n int) []RawEntry {
	r := rand.New(rand.NewPCG(1, 2))
	words := []string{"invoice", "report", "holiday", "march", "final", "draft", "IMG", "scan", "budget", "notes",
		"فاتورة", "تقرير", "café", "résumé", "project", "backup", "movie", "track", "photo", "2024"}
	exts := []string{"pdf", "txt", "jpg", "png", "mkv", "mp4", "flac", "docx", "go", "rs", "zip", "heic"}
	raw := []RawEntry{{Parent: -1, Name: "/bench", IsDir: true}}
	dirs := []int{0}
	for len(raw) < n {
		if len(raw)%50 == 0 {
			raw = append(raw, RawEntry{Parent: dirs[r.IntN(len(dirs))], Name: fmt.Sprintf("%s-%d", words[r.IntN(len(words))], len(raw)), IsDir: true})
			dirs = append(dirs, len(raw)-1)
			continue
		}
		name := fmt.Sprintf("%s %s %d.%s", words[r.IntN(len(words))], words[r.IntN(len(words))], r.IntN(10000), exts[r.IntN(len(exts))])
		raw = append(raw, RawEntry{Parent: dirs[len(dirs)-1-r.IntN(min(len(dirs), 20))], Name: name,
			Size: uint64(r.IntN(1 << 30)), Modified: 1_600_000_000 + int64(r.IntN(200_000_000))})
	}
	return raw
}

func BenchmarkSearch1M(b *testing.B) {
	raw := synthetic(1_000_000)
	t := time.Now()
	s := Build(raw)
	b.Logf("build %d entries: %v, ~%d MiB", s.Len(), time.Since(t), s.MemoryBytes()>>20)
	for _, q := range []string{"invoice march", "kind:video fsize:>500mb", "ext:pdf", "فاتوره", "report*", "re:^img_\\d+", "cafe 2024 -draft"} {
		pq, _ := query.ParseAt(q, 0)
		c, _ := query.Compile(pq, false)
		b.Run(q, func(b *testing.B) {
			var n uint64
			for i := 0; i < b.N; i++ {
				hits, err := s.Execute(c)
				if err != nil {
					b.Fatal(err)
				}
				s.Rank(hits, c, DefaultPage, 0)
				n = hits.GetCardinality()
			}
			b.ReportMetric(float64(n), "hits")
		})
	}
}
