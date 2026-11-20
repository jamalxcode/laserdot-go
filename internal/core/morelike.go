package core

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/media"
)

// MoreLike (the owner chose "more like this"): a search for files like the
// one at path — the same type, a similar size (half to twice) and modified
// within a month of it. It is ordinary query language, put in the search
// field, so its parts show as words and the choice row (Folder, Drive…)
// narrows it further. A folder gives a search inside it.
func (ld *Laserdot) MoreLike(path string) (string, error) {
	ld.mu.RLock()
	defer ld.mu.RUnlock()
	stores := []*index.Store{ld.store}
	for _, d := range ld.drives {
		stores = append(stores, d.store)
	}
	tries := []string{path}
	if real, err := filepath.EvalSymlinks(path); err == nil && real != path {
		tries = append(tries, real) // /var → /private/var on macOS
	}
	for _, s := range stores {
		var id uint32
		ok := false
		for _, p := range tries {
			if id, ok = s.Resolve(p); ok {
				break
			}
		}
		if !ok {
			continue
		}
		if s.IsDir(id) && s.Kind(id) != media.App {
			return "in:" + quoteValue(folderToken(path)), nil
		}
		var parts []string
		if ext := media.Extension(s.FoldedName(id)); ext != "" {
			parts = append(parts, "ext:"+quoteValue(ext))
		} else {
			parts = append(parts, "kind:"+s.Kind(id).String())
		}
		if size := s.Size(id); size > 0 {
			parts = append(parts, "fsize:"+sizeWord(size/2)+".."+sizeWord(size*2))
		}
		if m := s.Modified(id); m > 0 {
			t := time.Unix(m, 0).UTC()
			parts = append(parts, "modified:"+t.AddDate(0, 0, -30).Format("2006-01-02")+".."+t.AddDate(0, 0, 30).Format("2006-01-02"))
		}
		return strings.Join(parts, " "), nil
	}
	return "", fmt.Errorf("%s is not in the index", path)
}

// sizeWord: a size as the fsize: filter takes it, rounded to what reads
// well (kb under a megabyte, mb under a gigabyte, gb above).
func sizeWord(n uint64) string {
	switch {
	case n < 1_000:
		return fmt.Sprintf("%db", n)
	case n < 1_000_000:
		return fmt.Sprintf("%dkb", n/1_000)
	case n < 1_000_000_000:
		return fmt.Sprintf("%dmb", n/1_000_000)
	default:
		return fmt.Sprintf("%dgb", n/1_000_000_000)
	}
}
