package crypto_test

import (
	"bytes"
	"testing"

	"github.com/theizzatbek/gokit/crypto"
)

// FuzzMasterKeyOpen feeds arbitrary bytes to the at-rest blob parser.
// Open runs on stored ciphertext that an attacker with DB access can
// tamper with, so the hard contract is: it must NEVER panic — only
// return (plaintext, nil) on a valid blob or (nil, error) otherwise.
func FuzzMasterKeyOpen(f *testing.F) {
	mk, err := crypto.NewMasterKey(bytes.Repeat([]byte{0x01}, 32))
	if err != nil {
		f.Fatalf("NewMasterKey: %v", err)
	}

	sealed, err := mk.Seal([]byte("hello world"))
	if err != nil {
		f.Fatalf("Seal: %v", err)
	}
	f.Add(sealed)
	f.Add([]byte(""))
	f.Add([]byte("garbage"))
	f.Add(sealed[:len(sealed)-1])                       // truncated
	f.Add(append(append([]byte(nil), sealed...), 0xFF)) // trailing junk

	f.Fuzz(func(t *testing.T, blob []byte) {
		_, _ = mk.Open(blob)
	})
}
