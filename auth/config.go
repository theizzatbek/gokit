package auth

import "time"

// Alg selects the JWT signing algorithm. Asymmetric only — HS*/none deliberately
// unsupported.
type Alg int

const (
	AlgEdDSA Alg = iota // Ed25519. Default.
	AlgES256            // ECDSA P-256.
)

func (a Alg) String() string {
	switch a {
	case AlgEdDSA:
		return "EdDSA"
	case AlgES256:
		return "ES256"
	default:
		return "EdDSA"
	}
}

// Config is the required configuration for New. Tunables that are easy to get
// wrong (cookie domain, leeway, optional refresher) live on Option instead.
type Config struct {
	Issuer     string
	Audience   []string
	Keys       *KeySet
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	Leeway     time.Duration // 0 = use default (1 minute)

	// APIKeyHashSecret is the kit-side secret used by the APIKey
	// middleware to HMAC-SHA256 inbound API keys before lookup. A
	// DB dump alone won't reveal raw keys without this secret.
	//
	// Required iff the APIKey middleware is used; safe to omit when
	// only JWT (Bearer) is wired. Rotating the secret invalidates
	// every stored key hash — treat it like a long-lived signing
	// key, not an ephemeral session secret. 32 random bytes is the
	// recommended size.
	APIKeyHashSecret []byte
}
