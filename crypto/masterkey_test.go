package crypto

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	xerrs "github.com/theizzatbek/gokit/errs"
)

func mustRand32(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return b
}

func TestNewMasterKey_HappyPath(t *testing.T) {
	mk, err := NewMasterKey(mustRand32(t))
	if err != nil {
		t.Fatalf("NewMasterKey: %v", err)
	}
	if mk == nil {
		t.Fatal("MasterKey is nil")
	}
}

func TestNewMasterKey_BadLength(t *testing.T) {
	cases := []int{0, 1, 15, 16, 24, 31, 33, 64}
	for _, n := range cases {
		key := make([]byte, n)
		_, err := NewMasterKey(key)
		if err == nil {
			t.Errorf("NewMasterKey(%d bytes) err = nil, want CodeKeyLength", n)
			continue
		}
		var e *xerrs.Error
		if !errors.As(err, &e) || e.Code != CodeKeyLength {
			t.Errorf("NewMasterKey(%d bytes) err code = %v, want %q", n, codeOf(err), CodeKeyLength)
		}
	}
}

func TestNewMasterKeyFromBase64_EveryFlavour(t *testing.T) {
	raw := mustRand32(t)
	cases := map[string]string{
		"std-padded": base64.StdEncoding.EncodeToString(raw),
		"std-raw":    base64.RawStdEncoding.EncodeToString(raw),
		"url-padded": base64.URLEncoding.EncodeToString(raw),
		"url-raw":    base64.RawURLEncoding.EncodeToString(raw),
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			mk, err := NewMasterKeyFromBase64(encoded)
			if err != nil {
				t.Fatalf("NewMasterKeyFromBase64(%s): %v", encoded, err)
			}
			// Round-trip to prove the decoded key actually drives the AEAD.
			sealed, err := mk.Seal([]byte("plaintext"))
			if err != nil {
				t.Fatal(err)
			}
			got, err := mk.Open(sealed)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "plaintext" {
				t.Errorf("round-trip = %q, want plaintext", got)
			}
		})
	}
}

func TestNewMasterKeyFromBase64_BadString(t *testing.T) {
	_, err := NewMasterKeyFromBase64("not valid base64 !!! @#$")
	if err == nil {
		t.Fatal("expected error on garbage base64")
	}
	if codeOf(err) != CodeKeyBase64 {
		t.Errorf("code = %q, want %q", codeOf(err), CodeKeyBase64)
	}
}

func TestNewMasterKeyFromBase64_Empty(t *testing.T) {
	_, err := NewMasterKeyFromBase64("")
	if err == nil {
		t.Fatal("expected error on empty base64")
	}
	if codeOf(err) != CodeKeyBase64 {
		t.Errorf("code = %q, want %q", codeOf(err), CodeKeyBase64)
	}
}

func TestMasterKey_SealOpen_RoundTrip(t *testing.T) {
	mk, _ := NewMasterKey(mustRand32(t))
	plaintexts := [][]byte{
		nil,
		[]byte(""),
		[]byte("x"),
		[]byte("the quick brown fox jumps over the lazy dog"),
		bytes.Repeat([]byte("A"), 1024*16), // 16 KiB
	}
	for _, pt := range plaintexts {
		t.Run(label(pt), func(t *testing.T) {
			sealed, err := mk.Seal(pt)
			if err != nil {
				t.Fatalf("seal: %v", err)
			}
			if len(sealed) < 1+12 {
				t.Fatalf("sealed len %d, want ≥ 13 (version + nonce header)", len(sealed))
			}
			if sealed[0] != masterKeyVersion {
				t.Errorf("version byte = 0x%02x, want 0x%02x", sealed[0], masterKeyVersion)
			}
			got, err := mk.Open(sealed)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if !bytes.Equal(got, pt) {
				t.Errorf("round-trip mismatch")
			}
		})
	}
}

func TestMasterKey_Seal_NonceFreshPerCall(t *testing.T) {
	// Sealing the same plaintext twice MUST yield two distinct
	// blobs — nonce reuse is the catastrophic failure mode for
	// AES-GCM. This guards the contract that every Seal call draws
	// a fresh nonce.
	mk, _ := NewMasterKey(mustRand32(t))
	a, _ := mk.Seal([]byte("same"))
	b, _ := mk.Seal([]byte("same"))
	if bytes.Equal(a, b) {
		t.Fatal("two Seal calls of the same plaintext produced identical blobs (nonce reuse!)")
	}
	// Headers differ in the nonce slot specifically.
	if bytes.Equal(a[1:13], b[1:13]) {
		t.Fatal("nonce bytes identical across two Seal calls (catastrophic for AES-GCM)")
	}
}

func TestMasterKey_Open_TamperEveryByte(t *testing.T) {
	// Flipping any single byte of a sealed blob must produce an
	// error — the AEAD tag covers the entire ciphertext+aad, plus
	// the kit's version byte gates the prefix.
	mk, _ := NewMasterKey(mustRand32(t))
	sealed, _ := mk.Seal([]byte("the quick brown fox"))
	for i := 0; i < len(sealed); i++ {
		t.Run(label([]byte{byte(i)}), func(t *testing.T) {
			tampered := make([]byte, len(sealed))
			copy(tampered, sealed)
			tampered[i] ^= 0xff
			_, err := mk.Open(tampered)
			if err == nil {
				t.Errorf("Open of byte-%d-tampered blob succeeded; expected CodeCiphertext", i)
				return
			}
			if codeOf(err) != CodeCiphertext {
				t.Errorf("code = %q, want %q", codeOf(err), CodeCiphertext)
			}
		})
	}
}

func TestMasterKey_Open_RejectsShortBlobs(t *testing.T) {
	mk, _ := NewMasterKey(mustRand32(t))
	cases := [][]byte{
		nil,
		{},
		{0x01},
		{0x01, 0x00, 0x00, 0x00, 0x00}, // version + 4 nonce bytes (need 12)
		bytes.Repeat([]byte{0x01}, 12), // version + 11 nonce bytes
	}
	for _, blob := range cases {
		_, err := mk.Open(blob)
		if err == nil {
			t.Errorf("Open(short blob len=%d) returned nil err", len(blob))
			continue
		}
		if codeOf(err) != CodeCiphertext {
			t.Errorf("code = %q, want %q", codeOf(err), CodeCiphertext)
		}
	}
}

func TestMasterKey_Open_RejectsWrongVersion(t *testing.T) {
	mk, _ := NewMasterKey(mustRand32(t))
	sealed, _ := mk.Seal([]byte("hello"))
	sealed[0] = 0x02 // Keychain version
	_, err := mk.Open(sealed)
	if err == nil {
		t.Fatal("Open with foreign version byte succeeded")
	}
	if codeOf(err) != CodeCiphertext {
		t.Errorf("code = %q, want %q", codeOf(err), CodeCiphertext)
	}
	if !strings.Contains(err.Error(), "0x02") {
		t.Errorf("expected error to mention the offending version byte; got %q", err.Error())
	}
}

func TestMasterKey_Open_WrongKey(t *testing.T) {
	mkA, _ := NewMasterKey(mustRand32(t))
	mkB, _ := NewMasterKey(mustRand32(t))
	sealed, _ := mkA.Seal([]byte("hello"))
	_, err := mkB.Open(sealed)
	if err == nil {
		t.Fatal("Open with wrong key succeeded")
	}
	if codeOf(err) != CodeCiphertext {
		t.Errorf("code = %q, want %q", codeOf(err), CodeCiphertext)
	}
}

// label produces a short human-readable label for sub-tests so a
// failure log identifies the input without dumping the full bytes.
func label(b []byte) string {
	if len(b) == 0 {
		return "empty"
	}
	if len(b) == 1 {
		return "1-byte"
	}
	if len(b) > 50 {
		return "long"
	}
	return "n=" + itoa(len(b))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// codeOf returns the *errs.Error.Code if err is a *errs.Error, else
// "<unwrapped:T>" so test assertions report a useful hint when the
// wrong error shape comes through.
func codeOf(err error) string {
	var e *xerrs.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "<unwrapped>"
}
