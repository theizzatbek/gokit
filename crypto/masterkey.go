package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"

	"github.com/theizzatbek/gokit/errs"
)

// masterKeyVersion is the leading byte every MasterKey blob carries.
// 0x01 = AES-256-GCM, no kid. A future ciphersuite gets 0x03 with a
// matching constructor; Open paths stay format-isolated by version.
//
// 0x02 is reserved for Keychain blobs (which carry a kid byte after
// the version) so MasterKey.Open never accidentally decrypts a
// Keychain blob and vice versa.
const masterKeyVersion byte = 0x01

// MasterKey wraps a single AES-256-GCM AEAD initialised from a 32-byte
// key. Suitable for the static at-rest sealing case (refresh tokens,
// OAuth tokens, webhook secrets) where rotation is handled by
// re-sealing the row on read rather than by a kid-routed key map.
// Use [Keychain] when rotation requires both old and new keys to
// coexist.
//
// Wire format produced by [MasterKey.Seal] and consumed by
// [MasterKey.Open]:
//
//	[version=0x01] [nonce(12)] [ciphertext+tag(N+16)]
//
// The instance is safe for concurrent use.
type MasterKey struct {
	aead cipher.AEAD
}

// NewMasterKey returns a MasterKey from raw 32-byte key material.
// Returns [*errs.Error]{Code: [CodeKeyLength]} when len(key) != 32.
func NewMasterKey(key []byte) (*MasterKey, error) {
	if len(key) != 32 {
		return nil, errs.Validation(CodeKeyLength,
			fmt.Sprintf("crypto: MasterKey requires 32 bytes (AES-256), got %d", len(key)))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, CodeKeyLength,
			"crypto: aes cipher init")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, CodeKeyLength,
			"crypto: gcm init")
	}
	return &MasterKey{aead: aead}, nil
}

// NewMasterKeyFromBase64 is [NewMasterKey] with base64 decoding.
// Every Go stdlib base64 flavour is accepted (std / URL-safe, padded
// / raw) so operators copy-pasting from arbitrary key-management UIs
// don't have to remember which encoder produced the string. Decoded
// bytes must still be exactly 32.
//
// Returns [*errs.Error]{Code: [CodeKeyBase64]} on undecodable input,
// or [*errs.Error]{Code: [CodeKeyLength]} when the decoded length is
// wrong.
func NewMasterKeyFromBase64(s string) (*MasterKey, error) {
	raw, err := decodeAnyBase64(s)
	if err != nil {
		return nil, err
	}
	return NewMasterKey(raw)
}

// Seal returns a self-contained byte blob ready for at-rest storage:
//
//	[version=0x01] [nonce(12)] [ciphertext+tag(N+16)]
//
// The nonce is fresh per call (12 bytes drawn from
// [crypto/rand.Reader]). Nonce reuse is the catastrophic failure mode
// for AES-GCM — the kit never reuses a nonce because every Seal call
// draws a new one.
//
// Returns [*errs.Error]{Code: [CodeSealNonce]} when the system PRNG
// errors. The caller should NOT retry locally — the failure is
// kernel-level.
func (mk *MasterKey) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, mk.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, CodeSealNonce,
			"crypto: nonce read")
	}
	ct := mk.aead.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, 1+len(nonce)+len(ct))
	out = append(out, masterKeyVersion)
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

// Open consumes a [MasterKey.Seal] blob and returns the recovered
// plaintext. Every failure mode (short blob, unknown version,
// AEAD-tag mismatch) collapses into a single
// [*errs.Error]{Code: [CodeCiphertext]} — callers MUST NOT
// distinguish these on the wire (it leaks information to an attacker
// probing the blob format).
func (mk *MasterKey) Open(sealed []byte) ([]byte, error) {
	if len(sealed) < 1+mk.aead.NonceSize() {
		return nil, errs.Internal(CodeCiphertext,
			"crypto: sealed blob too short")
	}
	if sealed[0] != masterKeyVersion {
		return nil, errs.Internalf(CodeCiphertext,
			"crypto: MasterKey.Open got unknown version 0x%02x", sealed[0])
	}
	nonce := sealed[1 : 1+mk.aead.NonceSize()]
	ct := sealed[1+mk.aead.NonceSize():]
	pt, err := mk.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, CodeCiphertext,
			"crypto: aead open")
	}
	return pt, nil
}
