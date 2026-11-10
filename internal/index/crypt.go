package index

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/scrypt"
)

// Encrypted drive indexes (spec §9.8, redesigned for the Go port: no
// SQLCipher without cgo). The index on the drive is a sealed container:
//
//	"LDENC1\x00\x00" | salt (16) | log2 N (1) | r (1) | p (1) | nonce prefix (8)
//	then chunks: length (4, big endian, plaintext bytes) | AES-256-GCM sealed chunk
//
// The key comes from the passphrase with scrypt. Each chunk is sealed with
// nonce = prefix | chunk number (4 bytes), and its additional data says
// whether it is the last chunk, so chunks can't be reordered, dropped or
// truncated without the open failing. A wrong passphrase fails the first
// chunk: ErrLocked, and the file is never touched. The SQLite working copy
// lives on this Mac (owner-only), and is sealed back to the drive after
// every write.

var encMagic = []byte("LDENC1\x00\x00")

const (
	encChunk   = 1 << 20
	scryptLogN = 15
	scryptR    = 8
	scryptP    = 1
)

// IsSealed reports whether path holds a sealed index.
func IsSealed(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, len(encMagic))
	_, err = io.ReadFull(f, head)
	return err == nil && bytes.Equal(head, encMagic)
}

func deriveKey(pass string, salt []byte, logN, r, p int) ([]byte, error) {
	if logN < 10 || logN > 20 || r < 1 || r > 32 || p < 1 || p > 4 {
		return nil, errors.New("unsupported key parameters")
	}
	return scrypt.Key([]byte(pass), salt, 1<<logN, r, p, 32)
}

func chunkNonce(prefix []byte, n uint32) []byte {
	nonce := make([]byte, 12)
	copy(nonce, prefix)
	binary.BigEndian.PutUint32(nonce[8:], n)
	return nonce
}

// Seal encrypts the file plain into sealed (written atomically).
func Seal(plain, sealed, pass string) error {
	in, err := os.Open(plain)
	if err != nil {
		return err
	}
	defer in.Close()
	header := make([]byte, 0, len(encMagic)+16+3+8)
	header = append(header, encMagic...)
	salt := make([]byte, 16)
	prefix := make([]byte, 8)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	if _, err := rand.Read(prefix); err != nil {
		return err
	}
	header = append(header, salt...)
	header = append(header, scryptLogN, scryptR, scryptP)
	header = append(header, prefix...)
	key, err := deriveKey(pass, salt, scryptLogN, scryptR, scryptP)
	if err != nil {
		return err
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	tmp := sealed + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(out)
	w.Write(header)
	buf := make([]byte, encChunk)
	var n uint32
	r := bufio.NewReaderSize(in, encChunk)
	next, nerr := io.ReadFull(r, buf)
	for {
		if nerr != nil && !errors.Is(nerr, io.ErrUnexpectedEOF) && !errors.Is(nerr, io.EOF) {
			out.Close()
			os.Remove(tmp)
			return nerr
		}
		chunk := buf[:next]
		// Peek: is this the last chunk?
		after := make([]byte, encChunk)
		m, merr := 0, error(nil)
		if nerr == nil {
			m, merr = io.ReadFull(r, after)
		}
		last := nerr != nil || m == 0
		aad := []byte{0}
		if last {
			aad[0] = 1
		}
		var lenb [4]byte
		binary.BigEndian.PutUint32(lenb[:], uint32(len(chunk)))
		w.Write(lenb[:])
		w.Write(gcm.Seal(nil, chunkNonce(prefix, n), chunk, append(aad, lenb[:]...)))
		n++
		if last {
			break
		}
		buf, next, nerr = after, m, merr
	}
	if err := w.Flush(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	out.Close()
	return os.Rename(tmp, sealed)
}

// Unseal decrypts sealed into plain (owner-only). A wrong passphrase or a
// damaged file is ErrLocked / an error; plain is then left absent.
func Unseal(sealed, plain, pass string) error {
	in, err := os.Open(sealed)
	if err != nil {
		return err
	}
	defer in.Close()
	r := bufio.NewReaderSize(in, encChunk+64)
	header := make([]byte, len(encMagic)+16+3+8)
	if _, err := io.ReadFull(r, header); err != nil || !bytes.Equal(header[:len(encMagic)], encMagic) {
		return &UntrustedError{"not a sealed laserdot index"}
	}
	h := header[len(encMagic):]
	salt, params, prefix := h[:16], h[16:19], h[19:27]
	key, err := deriveKey(pass, salt, int(params[0]), int(params[1]), int(params[2]))
	if err != nil {
		return &UntrustedError{err.Error()}
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	os.MkdirAll(filepath.Dir(plain), 0o700)
	tmp := plain + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	fail := func(e error) error {
		out.Close()
		os.Remove(tmp)
		return e
	}
	w := bufio.NewWriter(out)
	for n := uint32(0); ; n++ {
		var lenb [4]byte
		if _, err := io.ReadFull(r, lenb[:]); err != nil {
			return fail(&UntrustedError{"truncated sealed index"})
		}
		size := binary.BigEndian.Uint32(lenb[:])
		if size > encChunk {
			return fail(&UntrustedError{"damaged sealed index"})
		}
		ct := make([]byte, int(size)+gcm.Overhead())
		if _, err := io.ReadFull(r, ct); err != nil {
			return fail(&UntrustedError{"truncated sealed index"})
		}
		nonce := chunkNonce(prefix, n)
		pt, err := gcm.Open(nil, nonce, ct, append([]byte{0}, lenb[:]...))
		last := false
		if err != nil {
			pt, err = gcm.Open(nil, nonce, ct, append([]byte{1}, lenb[:]...))
			last = true
		}
		if err != nil {
			if n == 0 {
				return fail(ErrLocked)
			}
			return fail(&UntrustedError{fmt.Sprintf("damaged sealed index (chunk %d)", n)})
		}
		w.Write(pt)
		if last {
			if _, err := r.Peek(1); err == nil {
				return fail(&UntrustedError{"data after the end of a sealed index"})
			}
			break
		}
	}
	if err := w.Flush(); err != nil {
		return fail(err)
	}
	out.Close()
	return os.Rename(tmp, plain)
}
