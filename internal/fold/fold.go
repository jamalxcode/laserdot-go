// Package fold is the single source of truth for text normalisation (spec
// §2.5): the indexer folds every name with Fold, and the query compiler folds
// every literal with it, so both sides always agree.
//
// Steps, in order:
//  1. NFD decomposition — splits é into e + accent and أ into ا + hamza.
//  2. Combining marks removed — Latin diacritics, Arabic harakat, shadda,
//     sukun, hamza above/below, superscript alef.
//  3. Arabic tatweel (ـ) removed.
//  4. Arabic letter variants mapped to a base letter (see ArabicLetter).
//  5. Arabic-Indic (٠–٩) and Extended Arabic-Indic (۰–۹) digits → ASCII.
//  6. Full Unicode case folding.
//  7. NFC recomposition.
package fold

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Fold folds s for matching. It is idempotent: Fold(Fold(s)) == Fold(s).
func Fold(s string) string {
	if isASCII(s) {
		return strings.ToLower(s)
	}
	// File names can hold arbitrary bytes; normalisation needs valid UTF-8.
	s = strings.ToValidUTF8(s, "\uFFFD")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFD.String(s) {
		if unicode.In(r, unicode.Mn, unicode.Mc, unicode.Me) || r == 'ـ' {
			continue
		}
		b.WriteRune(Digit(ArabicLetter(r)))
	}
	return norm.NFC.String(caseFold(b.String()))
}

// caseFold applies full case folding until it stops changing. x/text's
// folding is not idempotent for every script (Cherokee small letters fold to
// capitals, which fold back), and index and query must agree, so it runs to
// a fixed point — a second pass only changes anything for such scripts.
func caseFold(s string) string {
	// A cases.Caser is not safe for concurrent use: make one per call.
	c := cases.Fold()
	for i := 0; i < 4; i++ {
		next := c.String(s)
		if next == s {
			break
		}
		s = next
	}
	return s
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// ArabicLetter folds Arabic letter variants to one base letter. Hamza-carrying
// forms (أ إ آ ؤ ئ) are already handled by NFD and mark removal.
func ArabicLetter(r rune) rune {
	switch r {
	case 'ٱ': // alef wasla
		return 'ا'
	case 'ى', 'ی': // alef maqsura, Farsi ya → ya
		return 'ي'
	case 'ة': // ta marbuta → ha
		return 'ه'
	case 'ک': // keheh → kaf
		return 'ك'
	}
	return r
}

// Digit maps Arabic-Indic and Extended Arabic-Indic digits to ASCII.
func Digit(r rune) rune {
	switch {
	case r >= '٠' && r <= '٩':
		return '0' + (r - '٠')
	case r >= '۰' && r <= '۹':
		return '0' + (r - '۰')
	}
	return r
}

// PrepareQuery folds only digits — applied before parsing a query (spec
// §2.7) so numbers and dates can be typed with Arabic-Indic digits. It maps
// one rune to one rune, so error positions still match the text as typed;
// words get full folding later, through Fold.
func PrepareQuery(s string) string {
	return strings.Map(Digit, s)
}
