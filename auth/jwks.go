package auth

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// JWK is the on-the-wire JSON Web Key for one entry in the KeySet's
// verify set. Only the public half is exposed — private material never
// leaves the KeySet. Field set matches RFC 7517 for the two algorithms
// the kit supports:
//
//   - EdDSA (Ed25519) — `kty=OKP`, `crv=Ed25519`, `x=<base64url(pub)>`.
//   - ES256 (P-256)   — `kty=EC`,  `crv=P-256`,   `x`/`y=<base64url(coord)>`.
//
// `Alg` is always populated so consumers can pick the verification
// method without inspecting `kty`. `Use="sig"` is fixed — the kit
// only ever issues signing keys.
type JWK struct {
	KID string `json:"kid"`
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	X   string `json:"x"`
	Y   string `json:"y,omitempty"`
}

// JWKS is the standard JSON-encoded wrapper around a slice of JWK.
// Matches the `/.well-known/jwks.json` body shape.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// JWKS marshals every verify entry into a JWKS document. Order is not
// guaranteed (map iteration). Empty KeySet returns `{"keys":[]}` rather
// than null so dumb consumers don't crash.
//
// Returns *errs.Error{KindInternal,"jwks_encode_failed"} when an entry
// carries an unrecognised public-key type — the kit only writes
// ed25519.PublicKey / *ecdsa.PublicKey today, so a mismatch is a
// programmer error introduced by mutating the unexported KeySet.
func (ks *KeySet) JWKS() ([]byte, error) {
	out := JWKS{Keys: make([]JWK, 0, len(ks.verify))}
	for _, e := range ks.verify {
		jwk, err := pubToJWK(e)
		if err != nil {
			return nil, err
		}
		out.Keys = append(out.Keys, jwk)
	}
	return json.Marshal(out)
}

func pubToJWK(e signingKey) (JWK, error) {
	switch p := e.Pub.(type) {
	case ed25519.PublicKey:
		return JWK{
			KID: e.KID,
			Kty: "OKP",
			Crv: "Ed25519",
			Alg: "EdDSA",
			Use: "sig",
			X:   base64.RawURLEncoding.EncodeToString(p),
		}, nil
	case *ecdsa.PublicKey:
		// X / Y coordinates serialised as fixed-width 32-byte
		// big-endian per RFC 7518 §6.2.1.2 — leading zeros matter.
		size := (p.Curve.Params().BitSize + 7) / 8
		x := padLeft(p.X.Bytes(), size)
		y := padLeft(p.Y.Bytes(), size)
		return JWK{
			KID: e.KID,
			Kty: "EC",
			Crv: "P-256",
			Alg: "ES256",
			Use: "sig",
			X:   base64.RawURLEncoding.EncodeToString(x),
			Y:   base64.RawURLEncoding.EncodeToString(y),
		}, nil
	default:
		return JWK{}, xerrs.Internalf("jwks_encode_failed",
			"kid %q: unsupported public-key type %T", e.KID, e.Pub)
	}
}

func padLeft(b []byte, n int) []byte {
	if len(b) >= n {
		return b
	}
	out := make([]byte, n)
	copy(out[n-len(b):], b)
	return out
}

// JWKSHandler returns a Fiber handler that serves the kit's KeySet as a
// JWKS document under the standard `/.well-known/jwks.json` shape. The
// response is cached per the supplied max-age (seconds); zero / negative
// → `no-store` so consumers always re-fetch (use during rotation
// rollout). Default — 300s — is a reasonable balance between rotation
// latency and origin pressure.
//
// Mount programmatically:
//
//	app.Get("/.well-known/jwks.json", authObj.JWKSHandler(0))
//
// or via fibermap by wrapping the returned handler.
func (a *Auth[C]) JWKSHandler(maxAgeSeconds int) fiber.Handler {
	if maxAgeSeconds < 0 {
		maxAgeSeconds = 0
	}
	return func(c *fiber.Ctx) error {
		ks := a.eng.keySet()
		body, err := ks.JWKS()
		if err != nil {
			return err
		}
		if maxAgeSeconds == 0 {
			c.Set(fiber.HeaderCacheControl, "no-store")
		} else {
			c.Set(fiber.HeaderCacheControl, "public, max-age="+itoa(maxAgeSeconds))
		}
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		return c.Status(http.StatusOK).Send(body)
	}
}

// itoa avoids the strconv import bloat for this one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
