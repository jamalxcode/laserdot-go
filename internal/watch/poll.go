package watch

import (
	"hash/maphash"
	"path/filepath"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/scan"
)

// snapshot maps every folder under a root to a digest of its listing
// (children's names, kinds, inodes; sizes and times of files). Nil when the
// root cannot be read.
type snapshot map[string]uint64

var seed = maphash.MakeSeed()

func takeSnapshot(root string, opts scan.Options) snapshot {
	entries, _, err := scan.Walk(root, opts)
	if err != nil {
		return nil
	}
	s := make(snapshot, len(entries)/4+1)
	for _, e := range entries {
		if e.IsDir {
			if _, ok := s[e.Path]; !ok {
				s[e.Path] = 0
			}
		}
		if e.Parent < 0 {
			continue
		}
		var h maphash.Hash
		h.SetSeed(seed)
		h.WriteString(filepath.Base(e.Path))
		var buf [33]byte
		put := func(at int, v uint64) {
			for i := range 8 {
				buf[at+i] = byte(v >> (8 * i))
			}
		}
		put(0, e.Inode)
		if !e.IsDir { // a subfolder's own changes are its own entry
			put(8, e.Size)
			put(16, uint64(e.Modified))
		}
		put(24, uint64(len(e.Tags)))
		if e.IsDir {
			buf[32] = 1
		} else if e.IsSymlink {
			buf[32] = 2
		}
		h.Write(buf[:])
		// A sum is order-independent and cheap to fold per folder.
		s[filepath.Dir(e.Path)] += h.Sum64() | 1
	}
	return s
}

// pollLoop compares each root's snapshot with the previous one every
// interval and reports the folders whose listing changed. The first poll
// reports each root recursively (nothing to compare against yet: covers
// whatever changed before the baseline was taken), as does a root that
// appears or disappears.
func pollLoop(roots []string, interval time.Duration, opts scan.Options, q *queue, done <-chan struct{}) {
	prev := make([]snapshot, len(roots))
	for i, r := range roots {
		prev[i] = takeSnapshot(r, opts)
		select {
		case <-done:
			return
		default:
		}
	}
	first := true
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
		}
		for i, r := range roots {
			cur := takeSnapshot(r, opts)
			if first || (cur == nil) != (prev[i] == nil) {
				q.changed(r, true)
			} else {
				for dir, h := range cur {
					if old, ok := prev[i][dir]; !ok || old != h {
						q.changed(dir, false)
					}
				}
			}
			prev[i] = cur
			select {
			case <-done:
				return
			default:
			}
		}
		first = false
	}
}
