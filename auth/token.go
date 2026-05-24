package auth

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	xerrs "github.com/theizzatbek/fibermap/errs"
)

// engineConfig is the internal projection of auth.Config used by the token engine.
// auth.go constructs it from the public Config + Options.
type engineConfig struct {
	Keys     *KeySet
	Issuer   string
	Audience []string
	Leeway   time.Duration
	Now      func() time.Time
}

// engine is the internal Sign/Verify implementation. It is parameterised by
// the project's custom claim type C.
type engine[C any] struct {
	cfg engineConfig
}

func newEngine[C any](cfg engineConfig) *engine[C] {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &engine[C]{cfg: cfg}
}

// sign serializes claims and returns the JWT string. Header kid/alg are taken
// from the active KeySet entry. Issuer/Audience are populated from engineConfig
// when the caller did not set them explicitly.
func (e *engine[C]) sign(c Claims[C]) (string, error) {
	if c.Issuer == "" {
		c.Issuer = e.cfg.Issuer
	}
	if len(c.Audience) == 0 && len(e.cfg.Audience) > 0 {
		c.Audience = append([]string(nil), e.cfg.Audience...)
	}
	active := e.cfg.Keys.active
	if active.Priv == nil {
		return "", xerrs.Internal("no_active_signing_key", "key set has no active private key")
	}
	method, err := jwtMethodFor(active.Alg)
	if err != nil {
		return "", err
	}
	tok := jwt.NewWithClaims(method, claimsAdapter[C]{c: c})
	tok.Header["kid"] = active.KID
	out, err := tok.SignedString(signingMaterial(active))
	if err != nil {
		return "", xerrs.Wrapf(err, xerrs.KindInternal, "sign_failed", "jwt sign with alg %s", active.Alg)
	}
	return out, nil
}

// claimsAdapter wraps Claims[C] to satisfy jwt.Claims while routing JSON
// through Claims[C]'s own MarshalJSON.
type claimsAdapter[C any] struct{ c Claims[C] }

func (a claimsAdapter[C]) GetExpirationTime() (*jwt.NumericDate, error) {
	return numericDate(a.c.ExpiresAt), nil
}
func (a claimsAdapter[C]) GetIssuedAt() (*jwt.NumericDate, error) {
	return numericDate(a.c.IssuedAt), nil
}
func (a claimsAdapter[C]) GetNotBefore() (*jwt.NumericDate, error) {
	return numericDate(a.c.NotBefore), nil
}
func (a claimsAdapter[C]) GetIssuer() (string, error)  { return a.c.Issuer, nil }
func (a claimsAdapter[C]) GetSubject() (string, error) { return a.c.Subject, nil }
func (a claimsAdapter[C]) GetAudience() (jwt.ClaimStrings, error) {
	return jwt.ClaimStrings(a.c.Audience), nil
}

// MarshalJSON routes through Claims[C].MarshalJSON (flat custom merging).
func (a claimsAdapter[C]) MarshalJSON() ([]byte, error) { return a.c.MarshalJSON() }

func numericDate(unix int64) *jwt.NumericDate {
	if unix == 0 {
		return nil
	}
	return jwt.NewNumericDate(time.Unix(unix, 0))
}

func jwtMethodFor(alg string) (jwt.SigningMethod, error) {
	switch alg {
	case "EdDSA":
		return jwt.SigningMethodEdDSA, nil
	case "ES256":
		return jwt.SigningMethodES256, nil
	default:
		return nil, xerrs.Internalf("unsupported_alg", "unsupported signing alg %q", alg)
	}
}

func signingMaterial(s signingKey) any {
	switch k := s.Priv.(type) {
	case ed25519.PrivateKey:
		return k
	case *ecdsa.PrivateKey:
		return k
	default:
		return nil
	}
}

// Keep references to imported names that the Verify half (Task 6) will use.
var _ = errors.Is
var _ = fmt.Sprintf
