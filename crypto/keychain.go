package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"sort"

	"github.com/theizzatbek/gokit/errs"
)

// keychainVersion is the leading byte every Keychain blob carries.
// 0x02 = AES-256-GCM, kid-routed. Distinct from [masterKeyVersion]
// (0x01) so a single Open call on either type unambiguously rejects
// blobs of the other.
const keychainVersion byte = 0x02

// Keychain is a kid-routed multi-key sealer for the rotation case:
// callers that need both an old and a new key to coexist (old blobs
// stay decryptable while new blobs use the new key) initialise a
// Keychain with both keys and an "active" kid pointing at the new
// one. Seal always writes blobs under the active kid; Open routes
// the blob's embedded kid to the matching key in the map.
//
// Wire format produced by [Keychain.Seal] and consumed by
// [Keychain.Open]:
//
//	[version=0x02] [kid(1)] [nonce(12)] [ciphertext+tag(N+16)]
//
// kid is a single byte so a Keychain can hold up to 256 keys, which
// is more than any sane rotation policy will reach. The rotation
// workflow is:
//
//  1. Start the service with NewKeychain(active: 0x01, keys: {0x01: keyV1}).
//  2. New blobs use 0x01; old blobs decrypt fine.
//  3. Generate keyV2. Roll the service with NewKeychain(active: 0x02,
//     keys: {0x01: keyV1, 0x02: keyV2}). Now new blobs use 0x02;
//     existing 0x01 blobs still decrypt.
//  4. Background job re-seals every row, replacing 0x01 blobs with
//     0x02 blobs.
//  5. When the job completes, roll the service again with
//     NewKeychain(active: 0x02, keys: {0x02: keyV2}). Drop keyV1.
//
// The instance is safe for concurrent use after construction.
type Keychain struct {
	active byte
	aeads  map[byte]cipher.AEAD
}

// NewKeychain returns a Keychain over a kid-to-key map. active names
// the kid Seal will tag blobs with (it MUST be present in keys).
//
// Validation:
//   - keys must be non-empty                   → [CodeKeychainEmpty]
//   - active must be a key in keys             → [CodeKeychainNoActive]
//   - every value in keys must be 32 bytes     → [CodeKeyLength]
func NewKeychain(active byte, keys map[byte][]byte) (*Keychain, error) {
	if len(keys) == 0 {
		return nil, errs.Validation(CodeKeychainEmpty,
			"crypto: NewKeychain — keys map is empty")
	}
	if _, ok := keys[active]; !ok {
		return nil, errs.Validation(CodeKeychainNoActive,
			fmt.Sprintf("crypto: NewKeychain — active kid 0x%02x is not in keys map", active))
	}
	aeads := make(map[byte]cipher.AEAD, len(keys))
	// Iterate keys in sorted order so any per-key validation error
	// surfaces deterministically (small surface, but tests assume
	// stable error wording for short maps).
	kids := make([]byte, 0, len(keys))
	for kid := range keys {
		kids = append(kids, kid)
	}
	sort.Slice(kids, func(i, j int) bool { return kids[i] < kids[j] })
	for _, kid := range kids {
		key := keys[kid]
		if len(key) != 32 {
			return nil, errs.Validation(CodeKeyLength,
				fmt.Sprintf("crypto: Keychain key for kid 0x%02x — requires 32 bytes, got %d", kid, len(key)))
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, CodeKeyLength,
				fmt.Sprintf("crypto: aes cipher init for kid 0x%02x", kid))
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, CodeKeyLength,
				fmt.Sprintf("crypto: gcm init for kid 0x%02x", kid))
		}
		aeads[kid] = aead
	}
	return &Keychain{active: active, aeads: aeads}, nil
}

// NewKeychainFromBase64Map is [NewKeychain] with base64 decoding on
// each value. Every Go stdlib flavour is accepted per [decodeAnyBase64]
// (see [NewMasterKeyFromBase64] for the rationale).
//
// Failure modes mirror [NewKeychain] plus [CodeKeyBase64] when any
// value fails to decode. The error names the offending kid byte so
// operators can pinpoint which key entry needs fixing.
func NewKeychainFromBase64Map(active byte, b64Keys map[byte]string) (*Keychain, error) {
	rawKeys := make(map[byte][]byte, len(b64Keys))
	for kid, s := range b64Keys {
		raw, err := decodeAnyBase64(s)
		if err != nil {
			return nil, errs.Validation(CodeKeyBase64,
				fmt.Sprintf("crypto: Keychain key for kid 0x%02x — base64 decode failed: %v", kid, err))
		}
		rawKeys[kid] = raw
	}
	return NewKeychain(active, rawKeys)
}

// Seal returns a self-contained byte blob tagged with the active kid
// at construction time:
//
//	[version=0x02] [kid(1)] [nonce(12)] [ciphertext+tag(N+16)]
//
// Nonce semantics match [MasterKey.Seal] — fresh per call from
// [crypto/rand.Reader]. Returns [*errs.Error]{Code: [CodeSealNonce]}
// when the system PRNG errors.
func (kc *Keychain) Seal(plaintext []byte) ([]byte, error) {
	aead := kc.aeads[kc.active]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, CodeSealNonce,
			"crypto: nonce read")
	}
	ct := aead.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, 1+1+len(nonce)+len(ct))
	out = append(out, keychainVersion)
	out = append(out, kc.active)
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

// Open consumes a [Keychain.Seal] blob and returns the recovered
// plaintext. The blob's embedded kid byte routes to the matching key
// in the chain — kids absent from the chain, like every other open-
// failure mode, collapse into a single
// [*errs.Error]{Code: [CodeCiphertext]} so callers MUST NOT branch
// on the specific failure cause (information-leak to a probing
// attacker).
func (kc *Keychain) Open(sealed []byte) ([]byte, error) {
	// 1 version + 1 kid + 12 nonce = 14 byte minimum header.
	if len(sealed) < 14 {
		return nil, errs.Internal(CodeCiphertext,
			"crypto: sealed blob too short")
	}
	if sealed[0] != keychainVersion {
		return nil, errs.Internalf(CodeCiphertext,
			"crypto: Keychain.Open got unknown version 0x%02x", sealed[0])
	}
	kid := sealed[1]
	aead, ok := kc.aeads[kid]
	if !ok {
		return nil, errs.Internalf(CodeCiphertext,
			"crypto: Keychain.Open got unknown kid 0x%02x", kid)
	}
	nonceSize := aead.NonceSize()
	if len(sealed) < 2+nonceSize {
		return nil, errs.Internal(CodeCiphertext,
			"crypto: sealed blob too short for nonce")
	}
	nonce := sealed[2 : 2+nonceSize]
	ct := sealed[2+nonceSize:]
	pt, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, CodeCiphertext,
			"crypto: aead open")
	}
	return pt, nil
}

// ActiveKID returns the kid byte Seal will tag new blobs with.
// Useful for ops dashboards / preflight reports that surface
// "currently sealing under kid 0x02."
func (kc *Keychain) ActiveKID() byte { return kc.active }
