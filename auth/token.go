package auth

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	xerrs "github.com/theizzatbek/gokit/errs"
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

// verify parses tok, runs all required validations against engineConfig, and
// returns the typed Claims[C] on success. Errors carry the appropriate Code
// constant from errors.go so middleware can map them to a WWW-Authenticate
// challenge.
func (e *engine[C]) verify(tok string) (Claims[C], error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{e.cfg.Keys.activeAlg()}),
		// We control iss/aud/exp checks ourselves so we can return Code-coded errors.
		jwt.WithoutClaimsValidation(),
	)

	keyFunc := func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, xerrs.Unauthorized(CodeInvalidToken, "token missing kid header")
		}
		entry, ok := e.cfg.Keys.verifierFor(kid)
		if !ok {
			return nil, xerrs.Unauthorizedf(CodeKeyNotLoaded, "kid %q not loaded", kid)
		}
		if alg, _ := t.Header["alg"].(string); alg != entry.Alg {
			return nil, xerrs.Unauthorizedf(CodeInvalidToken, "alg mismatch: header %q vs key %q", alg, entry.Alg)
		}
		return entry.Pub, nil
	}

	var raw jwt.MapClaims
	_, err := parser.ParseWithClaims(tok, &raw, keyFunc)
	if err != nil {
		if existing, ok := errors.AsType[*xerrs.Error](err); ok {
			return Claims[C]{}, existing
		}
		return Claims[C]{}, xerrs.Wrap(err, xerrs.KindUnauthorized, CodeInvalidToken, "token verification failed")
	}

	// Hand the body back through Claims[C].UnmarshalJSON so generic Custom is filled.
	body, err := jsonMarshalMap(raw)
	if err != nil {
		return Claims[C]{}, xerrs.Wrap(err, xerrs.KindInternal, "claims_decode_failed", "re-marshal claims")
	}
	var out Claims[C]
	if err := out.UnmarshalJSON(body); err != nil {
		return Claims[C]{}, xerrs.Wrap(err, xerrs.KindInternal, "claims_decode_failed", "decode claims")
	}

	now := e.cfg.Now()
	if out.ExpiresAt == 0 {
		return Claims[C]{}, xerrs.Unauthorized(CodeInvalidToken, "token missing exp")
	}
	if time.Unix(out.ExpiresAt, 0).Before(now.Add(-e.cfg.Leeway)) {
		return Claims[C]{}, xerrs.Unauthorized(CodeExpiredToken, "token expired")
	}
	if out.NotBefore != 0 && time.Unix(out.NotBefore, 0).After(now.Add(e.cfg.Leeway)) {
		return Claims[C]{}, xerrs.Unauthorized(CodeInvalidToken, "token not yet valid")
	}
	if e.cfg.Issuer != "" && out.Issuer != e.cfg.Issuer {
		return Claims[C]{}, xerrs.Unauthorizedf(CodeInvalidToken, "issuer %q rejected", out.Issuer)
	}
	if len(e.cfg.Audience) > 0 && !intersects(out.Audience, e.cfg.Audience) {
		return Claims[C]{}, xerrs.Unauthorized(CodeInvalidToken, "audience mismatch")
	}
	return out, nil
}

func intersects(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

func jsonMarshalMap(m jwt.MapClaims) ([]byte, error) {
	return json.Marshal(map[string]any(m))
}
