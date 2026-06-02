package storepg

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"

	"github.com/theizzatbek/gokit/clients/webhooks"
	"github.com/theizzatbek/gokit/errs"
)

// cryptoVersion is the leading byte in every sealed blob. v1 = 0x01.
// On a future ciphersuite change, increment to 0x02 and add a
// branch in open(); seal() always writes the latest version.
const cryptoVersion byte = 0x01

// crypto wraps an AES-256-GCM AEAD initialised from a 32-byte key.
// One instance is shared across all store operations (AEAD is
// goroutine-safe).
type crypto struct {
	aead cipher.AEAD
}

func newCrypto(key []byte) (*crypto, error) {
	if len(key) != 32 {
		return nil, errs.Validation(webhooks.CodeStorepgNoKey,
			fmt.Sprintf("webhooks: secret key must be 32 bytes, got %d", len(key)))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, webhooks.CodeStorepgNoKey,
			"webhooks: aes cipher init")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, webhooks.CodeStorepgNoKey,
			"webhooks: gcm init")
	}
	return &crypto{aead: aead}, nil
}

// seal returns [version(1)][nonce(12)][ciphertext+tag(N)].
func (c *crypto) seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, webhooks.CodeStorepgDecryptFailed,
			"webhooks: nonce read")
	}
	ct := c.aead.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, 1+len(nonce)+len(ct))
	out = append(out, cryptoVersion)
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

func (c *crypto) open(sealed []byte) ([]byte, error) {
	if len(sealed) < 1+c.aead.NonceSize() {
		return nil, errs.Internal(webhooks.CodeStorepgDecryptFailed,
			"webhooks: sealed too short")
	}
	if sealed[0] != cryptoVersion {
		return nil, errs.Internalf(webhooks.CodeStorepgDecryptFailed,
			"webhooks: unknown crypto version 0x%02x", sealed[0])
	}
	nonce := sealed[1 : 1+c.aead.NonceSize()]
	ct := sealed[1+c.aead.NonceSize():]
	pt, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, webhooks.CodeStorepgDecryptFailed,
			"webhooks: gcm open")
	}
	return pt, nil
}
