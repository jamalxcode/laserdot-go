package core

import (
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jamalxcode/laserdot-go/internal/fold"
	"github.com/jamalxcode/laserdot-go/internal/index"
	"github.com/jamalxcode/laserdot-go/internal/media"
)

// Snippet (v1.25): the passage of a document where the words were found,
// for the preview of a result found by its contents. The content index keeps
// no copy of the text (spec §2.4), so the one file shown is read again, the
// way content indexing reads it. "" when no word is found or the file is not
// a readable document.
func (ld *Laserdot) Snippet(path string, words []string) string {
	if p, err := canonical(path); err == nil {
		path = p
	}
	kind := index.ContentTypeOf(media.Extension(fold.Fold(path)))
	if kind == index.ContentNone {
		return ""
	}
	text, ok := snippetText(path, kind)
	if !ok {
		return ""
	}
	var folded []string
	for _, w := range words {
		if w = fold.Fold(strings.Trim(w, `"*?`)); w != "" {
			folded = append(folded, w)
		}
	}
	if len(folded) == 0 {
		return ""
	}
	// The first line holding the most of the words.
	best, bestScore, bestAt := "", 0, 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fl := fold.Fold(line)
		score, at := 0, -1
		for _, w := range folded {
			if i := strings.Index(fl, w); i >= 0 {
				score++
				if at < 0 || i < at {
					at = i
				}
			}
		}
		if score > bestScore {
			best, bestScore, bestAt = line, score, at
			if score == len(folded) {
				break
			}
		}
	}
	if bestScore == 0 {
		return ""
	}
	return around(best, bestAt, 220)
}

// around trims line to about n characters around byte offset at (an offset
// in its folded form, close enough for placing an ellipsis).
func around(line string, at, n int) string {
	r := []rune(line)
	if len(r) <= n {
		return line
	}
	pos := utf8.RuneCountInString(line[:min(at, len(line))])
	start := max(0, pos-n/3)
	end := min(len(r), start+n)
	start = max(0, end-n)
	out := string(r[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(r) {
		out += "…"
	}
	return out
}

// Reading a big PDF or Word file can take a while, and arrowing through
// results asks for one passage after another: the text of the last few
// documents is kept in memory (by path, size and date), and a document not
// read within snippetWait gives no passage this time — its reading goes on,
// so selecting it again shows the passage at once.
const (
	snippetWait      = 400 * time.Millisecond
	snippetCacheDocs = 8
	snippetCacheMax  = 16 << 20 // bytes of text
)

type snippetKey struct {
	path  string
	size  int64
	mtime int64
}

type snippetDoc struct {
	text string
	ok   bool
	used time.Time
}

var snippetCache = struct {
	sync.Mutex
	docs    map[snippetKey]*snippetDoc
	reading map[snippetKey]chan struct{}
}{docs: map[snippetKey]*snippetDoc{}, reading: map[snippetKey]chan struct{}{}}

func snippetText(path string, kind index.ContentType) (string, bool) {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return "", false
	}
	key := snippetKey{path, fi.Size(), fi.ModTime().UnixNano()}
	c := &snippetCache
	c.Lock()
	if d, ok := c.docs[key]; ok {
		d.used = time.Now()
		c.Unlock()
		return d.text, d.ok
	}
	done, busy := c.reading[key]
	if !busy {
		done = make(chan struct{})
		c.reading[key] = done
		go func() {
			text, ok := index.RawText(path, kind)
			c.Lock()
			defer c.Unlock()
			delete(c.reading, key)
			if len(text) <= snippetCacheMax {
				c.docs[key] = &snippetDoc{text: text, ok: ok, used: time.Now()}
				trimSnippetCache()
			}
			close(done)
		}()
	}
	c.Unlock()
	select {
	case <-done:
	case <-time.After(snippetWait):
		return "", false
	}
	c.Lock()
	defer c.Unlock()
	if d, ok := c.docs[key]; ok {
		return d.text, d.ok
	}
	return "", false
}

// trimSnippetCache drops the least recently used documents beyond the
// limits. The lock is held.
func trimSnippetCache() {
	c := &snippetCache
	for {
		total := 0
		var oldest snippetKey
		var oldestAt time.Time
		for k, d := range c.docs {
			total += len(d.text)
			if oldestAt.IsZero() || d.used.Before(oldestAt) {
				oldest, oldestAt = k, d.used
			}
		}
		if len(c.docs) <= snippetCacheDocs && total <= snippetCacheMax {
			return
		}
		delete(c.docs, oldest)
	}
}
