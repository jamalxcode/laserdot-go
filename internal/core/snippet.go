package core

import (
	"strings"
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
	text, ok := index.RawText(path, kind)
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
