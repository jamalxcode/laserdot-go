package index

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSealUnsealRoundTrip(t *testing.T) {
	dir := t.TempDir()
	for _, size := range []int{0, 10, encChunk, encChunk + 1, 2*encChunk + 5} {
		data := make([]byte, size)
		rand.Read(data)
		plain, sealed, back := filepath.Join(dir, "p"), filepath.Join(dir, "s"), filepath.Join(dir, "b")
		os.WriteFile(plain, data, 0o600)
		if err := Seal(plain, sealed, "correct horse"); err != nil {
			t.Fatal(err)
		}
		if !IsSealed(sealed) {
			t.Fatal("not sealed")
		}
		if err := Unseal(sealed, back, "correct horse"); err != nil {
			t.Fatal(size, err)
		}
		got, _ := os.ReadFile(back)
		if !bytes.Equal(got, data) {
			t.Fatal(size, "round trip")
		}
		if err := Unseal(sealed, back+"2", "wrong"); !errors.Is(err, ErrLocked) {
			t.Error(size, err)
		}
		if _, err := os.Stat(back + "2"); err == nil {
			t.Error("plain file left behind")
		}
		// Truncation is detected.
		b, _ := os.ReadFile(sealed)
		if len(b) > 64 {
			os.WriteFile(sealed, b[:len(b)-1], 0o600)
			if err := Unseal(sealed, back+"3", "correct horse"); err == nil {
				t.Error(size, "truncated container opened")
			}
		}
	}
}
