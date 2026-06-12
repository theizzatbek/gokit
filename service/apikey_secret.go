package service

import (
	"encoding/base64"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// minAPIKeyHashSecretBytes is the HMAC-SHA256 best-practice floor
// for the APIKey HMAC pepper. Anything shorter weakens the
// derived `keyHash` enough that the kit refuses it at boot rather
// than risk pushing a brittle secret to production.
const minAPIKeyHashSecretBytes = 32

// decodeAPIKeyHashSecret accepts every base64 flavour Go's stdlib
// knows: standard / URL-safe, padded / raw. It only fails when the
// input is neither — operators copy-pasting from key-management UIs
// see whatever flavour those use Just Work.
//
// On success returns the raw decoded bytes when the length is
// ≥ minAPIKeyHashSecretBytes. Empty input is accepted and surfaces
// to the caller as a nil slice (which buildAuth treats as "no
// pepper supplied" — the auth.Auth value is built without one, and
// the APIKey middleware would panic at construction time if it
// were ever wired).
func decodeAPIKeyHashSecret(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	var (
		raw []byte
		err error
	)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.RawURLEncoding,
	} {
		raw, err = enc.DecodeString(s)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, xerrs.Validation(CodeAuthInvalidAPIKeyHashSecret,
			"service: AUTH_APIKEY_HASH_SECRET — not valid base64 (std/url, padded/raw all tried)")
	}
	if len(raw) < minAPIKeyHashSecretBytes {
		return nil, xerrs.Validationf(CodeAuthInvalidAPIKeyHashSecret,
			"service: AUTH_APIKEY_HASH_SECRET — decoded length %d, need ≥ %d (HMAC-SHA256 best practice)",
			len(raw), minAPIKeyHashSecretBytes)
	}
	return raw, nil
}
