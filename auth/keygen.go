package auth

import (
	"crypto/rand"
	"encoding/base64"

	"github.com/theizzatbek/gokit/errs"
)

// APIKeyPrefix is the canonical kit prefix for issued API keys. The
// "ak" marker stands for "API key"; the trailing underscore separates
// it from the random tail so log greps and admin UI columns can
// reliably parse `<prefix>_<tail>` shapes.
//
// Format mirrors the GitHub PAT-classic shape (ghp_…) and pairs with
// Stripe's prefix-as-scope-marker convention (sk_live_… vs sk_test_…)
// at a shorter length: kit doesn't bake environment scope into the
// prefix because that mapping lives in [KeyStore] metadata, not in
// the key string itself.
const APIKeyPrefix = "ak_"

// apiKeyRandBytes is the byte count drawn from the system PRNG for
// the random tail. 21 bytes encode cleanly to 28 base64-RawURL
// characters (21 * 4 / 3 = 28) — chosen so the tail length is fixed
// and the prefix arithmetic in GenerateAPIKey + Prefix is exact.
const apiKeyRandBytes = 21

// apiKeyPrefixLen is the number of leading characters of the issued
// plain key that are safe to surface in admin UIs (key listings,
// audit logs) without revealing enough material to brute-force the
// remainder. 8 = 3-char [APIKeyPrefix] + 5 chars from the random tail.
const apiKeyPrefixLen = 8

// minPepperBytes is the kit's HMAC-SHA256 best-practice floor for the
// pepper. Mirrors the [service.minAPIKeyHashSecretBytes] check inside
// service.New so callers building Auth by hand surface the same misconfig
// at the same boundary.
const minPepperBytes = 32

// Stable error codes returned in [*errs.Error.Code] from
// [GenerateAPIKey]. Match the rest of the auth package's APIKey-family
// codes so downstream alerting can branch consistently.
const (
	// CodeKeygenBadPepper — pepper bytes shorter than 32 (the
	// HMAC-SHA256 best-practice floor). KindValidation.
	CodeKeygenBadPepper = "auth_keygen_bad_pepper"

	// CodeKeygenEntropy — system PRNG returned an error reading
	// the random tail. KindInternal. Kernel-level failure
	// (`/dev/urandom` blocked etc.); surface as 503 to upstream
	// without retrying locally.
	CodeKeygenEntropy = "auth_keygen_entropy"
)

// GenerateAPIKey mints a fresh plain API key, its lookup hash, and a
// safe-to-display prefix in a single call. Use at admin / KeyStore-
// insert time; the plain key is the only place the raw material ever
// flows in cleartext — caller MUST show it to the human once and
// then drop it. Store hash + prefix in the [KeyStore]; subsequent
// authentications hash the inbound key with the same pepper and
// match against the stored hash.
//
// Returns:
//
//	plain  — "ak_<28 url-safe-no-padding base64 chars>", 31 chars total.
//	         Show the user once at issue time; never store.
//	hash   — HMAC-SHA256(plain, pepper), 32 bytes. Store in the
//	         KeyStore's keyHash column. [Auth.APIKey] middleware
//	         derives the same hash at verify time via [HashAPIKey]
//	         with the same pepper.
//	prefix — first 8 chars of plain ("ak_xxxxx"). Safe to surface
//	         in admin UIs / audit logs as a stable identifier of the
//	         key without revealing enough to brute-force the remaining
//	         23 chars.
//
// pepper MUST equal [Config.APIKeyHashSecret] (or the override from
// [WithAPIKeyHashSecret]) — otherwise the issued key won't resolve
// at login time, because [Auth.APIKey] hashes inbound plain keys with
// the configured pepper and the lookup will miss.
//
// Errors:
//
//	*errs.Error{Code: [CodeKeygenBadPepper]} — pepper < 32 bytes.
//	*errs.Error{Code: [CodeKeygenEntropy]}   — system PRNG failure.
func GenerateAPIKey(pepper []byte) (plain string, hash []byte, prefix string, err error) {
	if len(pepper) < minPepperBytes {
		return "", nil, "", errs.Validationf(CodeKeygenBadPepper,
			"auth: GenerateAPIKey pepper length %d, want ≥ %d (HMAC-SHA256 best practice)",
			len(pepper), minPepperBytes)
	}
	raw := make([]byte, apiKeyRandBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, "", errs.Wrap(err, errs.KindInternal, CodeKeygenEntropy,
			"auth: GenerateAPIKey rand read")
	}
	tail := base64.RawURLEncoding.EncodeToString(raw)
	if len(tail) != 28 {
		// Defensive — base64.RawURLEncoding always produces exactly
		// (apiKeyRandBytes*4+2)/3 chars for the chosen byte count,
		// so this only fires on a stdlib bug. Surface as a generic
		// entropy-class error rather than panic.
		return "", nil, "", errs.Internalf(CodeKeygenEntropy,
			"auth: GenerateAPIKey base64 tail length %d, want 28 (stdlib bug?)", len(tail))
	}
	plain = APIKeyPrefix + tail
	hash = HashAPIKey(plain, pepper)
	prefix = plain[:apiKeyPrefixLen]
	return plain, hash, prefix, nil
}
