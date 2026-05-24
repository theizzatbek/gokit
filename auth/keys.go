package auth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/theizzatbek/fibermap/errs"
)

// signingKey is one entry in a KeySet. Priv is nil for verify-only keys.
type signingKey struct {
	KID  string
	Alg  string // "EdDSA" | "ES256"
	Priv crypto.Signer
	Pub  crypto.PublicKey
}

// KeySet holds the active signing key plus every key still trusted for
// verification (during rotation, the old key stays here until grace expires).
type KeySet struct {
	active signingKey
	verify map[string]signingKey
}

// LoadKeysFromPEM parses a map of kid -> PEM bytes. A PEM block may carry a
// PKCS#8 private key (its public half is derived; the kid is usable for both
// sign and verify) or a SubjectPublicKeyInfo public key (verify-only).
//
// activeKID names the key used for signing new tokens. It may be "" if every
// loaded key is public-only (verify-only mode — useful for receiver-side
// services that never sign). If activeKID names a public-only key, an error is
// returned.
func LoadKeysFromPEM(activeKID string, pems map[string][]byte) (*KeySet, error) {
	ks := &KeySet{verify: make(map[string]signingKey, len(pems))}
	for kid, raw := range pems {
		block, _ := pem.Decode(raw)
		if block == nil {
			return nil, errs.Validationf("invalid_pem", "kid %q: not a valid PEM block", kid)
		}
		entry, err := parsePEMBlock(kid, block)
		if err != nil {
			return nil, err
		}
		ks.verify[kid] = entry
	}
	if activeKID == "" {
		return ks, nil
	}
	entry, ok := ks.verify[activeKID]
	if !ok {
		return nil, errs.Validationf("invalid_active_kid", "active kid %q not present in key map", activeKID)
	}
	if entry.Priv == nil {
		return nil, errs.Validationf("active_kid_public_only", "active kid %q is verify-only (no private key in PEM)", activeKID)
	}
	ks.active = entry
	return ks, nil
}

// GenerateEd25519Key produces a fresh in-memory KeySet with one Ed25519 keypair.
// Intended for dev/test wiring; production should load persistent PEMs.
func GenerateEd25519Key(kid string) (*KeySet, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "ed25519_generate_failed", "ed25519 keygen")
	}
	entry := signingKey{KID: kid, Alg: "EdDSA", Priv: priv, Pub: pub}
	return &KeySet{
		active: entry,
		verify: map[string]signingKey{kid: entry},
	}, nil
}

func parsePEMBlock(kid string, block *pem.Block) (signingKey, error) {
	switch block.Type {
	case "PRIVATE KEY", "PKCS8 PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return signingKey{}, errs.Wrapf(err, errs.KindValidation, "invalid_pem", "kid %q: parse PKCS8", kid)
		}
		return privateToEntry(kid, k)
	case "PUBLIC KEY":
		k, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return signingKey{}, errs.Wrapf(err, errs.KindValidation, "invalid_pem", "kid %q: parse SPKI", kid)
		}
		return publicToEntry(kid, k)
	default:
		return signingKey{}, errs.Validationf("invalid_pem", "kid %q: unsupported PEM type %q", kid, block.Type)
	}
}

func privateToEntry(kid string, k any) (signingKey, error) {
	switch priv := k.(type) {
	case ed25519.PrivateKey:
		return signingKey{KID: kid, Alg: "EdDSA", Priv: priv, Pub: priv.Public()}, nil
	case *ecdsa.PrivateKey:
		if priv.Curve != elliptic.P256() {
			return signingKey{}, errs.Validationf("unsupported_curve", "kid %q: only P-256 ECDSA supported", kid)
		}
		return signingKey{KID: kid, Alg: "ES256", Priv: priv, Pub: &priv.PublicKey}, nil
	default:
		return signingKey{}, errs.Validationf("unsupported_key_type", "kid %q: private key type %T not supported", kid, k)
	}
}

func publicToEntry(kid string, k any) (signingKey, error) {
	switch pub := k.(type) {
	case ed25519.PublicKey:
		return signingKey{KID: kid, Alg: "EdDSA", Pub: pub}, nil
	case *ecdsa.PublicKey:
		if pub.Curve != elliptic.P256() {
			return signingKey{}, errs.Validationf("unsupported_curve", "kid %q: only P-256 ECDSA supported", kid)
		}
		return signingKey{KID: kid, Alg: "ES256", Pub: pub}, nil
	default:
		return signingKey{}, errs.Validationf("unsupported_key_type", "kid %q: public key type %T not supported", kid, k)
	}
}

// verifierFor returns the entry registered under kid (verify-only or full).
// Returns ok=false when the kid is unknown — callers should map this to
// CodeKeyNotLoaded.
func (ks *KeySet) verifierFor(kid string) (signingKey, bool) {
	e, ok := ks.verify[kid]
	return e, ok
}

// activeAlg returns the algorithm header value to put on freshly-signed tokens.
func (ks *KeySet) activeAlg() string { return ks.active.Alg }

// Workaround for unused-imports lint when the package is empty otherwise.
var _ = fmt.Sprintf
