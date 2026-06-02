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
	if sealed[0] != cryptoVersion {
		t.Fatalf("version byte = %d, want %d", sealed[0], cryptoVersion)
	}
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
