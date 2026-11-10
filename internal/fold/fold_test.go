package fold

import "testing"

func eq(t *testing.T, got, want, ctx string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %q, want %q", ctx, got, want)
	}
}

func TestASCIILowercases(t *testing.T) {
	eq(t, Fold("Report(2).PDF"), "report(2).pdf", "ascii")
}

func TestLatinDiacritics(t *testing.T) {
	eq(t, Fold("Café Crème"), "cafe creme", "cafe")
	eq(t, Fold("Ångström"), "angstrom", "angstrom")
}

func TestFullCaseFolding(t *testing.T) {
	eq(t, Fold("Straße"), "strasse", "sharp s")
	eq(t, Fold("ΣΊΣΥΦΟΣ"), Fold("σίσυφος"), "greek")
}

func TestArabicAlefVariants(t *testing.T) {
	for _, v := range []string{"أحمد", "إحمد", "آحمد", "ٱحمد", "احمد"} {
		eq(t, Fold(v), "احمد", v)
	}
}

func TestArabicHarakatAndTatweel(t *testing.T) {
	eq(t, Fold("مُحَمَّد"), "محمد", "harakat")
	eq(t, Fold("محـــمد"), "محمد", "tatweel")
}

func TestArabicLetterVariants(t *testing.T) {
	eq(t, Fold("مدرسة"), Fold("مدرسه"), "ta marbuta")
	eq(t, Fold("مستشفى"), Fold("مستشفي"), "alef maqsura")
	eq(t, Fold("مؤتمر"), "موتمر", "waw hamza")
	eq(t, Fold("رئيس"), "رييس", "ya hamza")
}

func TestDigits(t *testing.T) {
	eq(t, Fold("فاتورة ٢٠٢٤"), Fold("فاتوره 2024"), "arabic-indic")
	eq(t, Fold("۴"), "4", "extended")
	eq(t, PrepareQuery("fsize:>٤gb"), "fsize:>4gb", "prepare")
}

func TestIdempotent(t *testing.T) {
	for _, s := range []string{"Café", "أحمد ٤", "Straße", "مُحَمَّد", "ﬁle"} {
		once := Fold(s)
		eq(t, Fold(once), once, s)
	}
}

func TestInvalidUTF8AndCherokee(t *testing.T) {
	// Found by fuzzing: invalid bytes stopped decomposition of the next
	// letter, and Cherokee small letters fold to capitals and back.
	for _, s := range []string{"0\xf2Ӓ", "0ꮜ", "Ꮜ"} {
		once := Fold(s)
		eq(t, Fold(once), once, s)
	}
	eq(t, Fold("\xffcafé"), "\uFFFDcafe", "invalid byte becomes U+FFFD")
}

func FuzzFoldIdempotent(f *testing.F) {
	for _, s := range []string{"Café", "أحمد ٤", "Straße", "ﬁle", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		once := Fold(s)
		if Fold(once) != once {
			t.Errorf("not idempotent for %q: %q then %q", s, once, Fold(once))
		}
	})
}
