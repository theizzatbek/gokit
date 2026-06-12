package storepg

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestSealOpen_Roundtrip(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)

	c, err := newCrypto(key)
	if err != nil {
		t.Fatalf("newCrypto: %v", err)
	}
	plaintext := []byte("super-secret")
	sealed, err := c.seal(plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	// Wire-format version-byte invariant lives in the gokit/crypto
	// package's own tests now (the wrapper delegates straight through);
	// the round-trip assertion below is what this package owns.
	opened, err := c.open(sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("roundtrip mismatch: %s", opened)
	}
}

func TestNewCrypto_BadKeyLen(t *testing.T) {
	if _, err := newCrypto([]byte("short")); err == nil {
		t.Fatal("expected error on short key")
	}
}
