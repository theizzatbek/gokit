package auth

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/theizzatbek/fibermap/errs"
)

var jwtBase64 = base64.RawURLEncoding

func newTestEngine(t *testing.T) *engine[testClaims] {
	t.Helper()
	ks, err := GenerateEd25519Key("k1")
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	return newEngine[testClaims](engineConfig{
		Keys:     ks,
		Issuer:   "myapp",
		Audience: []string{"web"},
		Leeway:   time.Minute,
		Now:      func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
}

func TestSign_ProducesParseableJWTWithKID(t *testing.T) {
	e := newTestEngine(t)
	tok, err := e.sign(Claims[testClaims]{
		Subject:   "u-1",
		ExpiresAt: 1_700_000_900,
		IssuedAt:  1_700_000_000,
		Custom:    testClaims{TenantID: "t-9"},
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3-segment JWT, got %d: %q", len(parts), tok)
	}
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	parsed, _, err := parser.ParseUnverified(tok, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Header["kid"] != "k1" {
		t.Errorf("header kid = %v, want k1", parsed.Header["kid"])
	}
	if parsed.Header["alg"] != "EdDSA" {
		t.Errorf("header alg = %v, want EdDSA", parsed.Header["alg"])
	}
}

func TestSign_PopulatesIssuerAudienceFromEngineWhenClaimsBlank(t *testing.T) {
	e := newTestEngine(t)
	tok, _ := e.sign(Claims[testClaims]{Subject: "u-1", ExpiresAt: 1, IssuedAt: 1})
	parsed, _, _ := jwt.NewParser(jwt.WithoutClaimsValidation()).ParseUnverified(tok, jwt.MapClaims{})
	body := parsed.Claims.(jwt.MapClaims)
	if body["iss"] != "myapp" {
		t.Errorf("iss = %v, want myapp", body["iss"])
	}
	auds, _ := body["aud"].([]any)
	if len(auds) != 1 || auds[0] != "web" {
		t.Errorf("aud = %v, want [web]", body["aud"])
	}
}

func TestVerify_RoundTrip(t *testing.T) {
	e := newTestEngine(t)
	tok, _ := e.sign(Claims[testClaims]{
		Subject: "u-1", IssuedAt: 1_700_000_000, ExpiresAt: 1_700_000_900,
		Scopes: []string{"a"}, Custom: testClaims{TenantID: "t-9"},
	})
	got, err := e.verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.Subject != "u-1" || got.Custom.TenantID != "t-9" {
		t.Fatalf("Claims = %#v", got)
	}
}

func TestVerify_RejectsAlgConfusion(t *testing.T) {
	e := newTestEngine(t)
	// Forge a header that claims HS256 but use the same body — the engine must
	// refuse because its KeySet only knows EdDSA.
	tok, _ := e.sign(Claims[testClaims]{Subject: "u", IssuedAt: 1_700_000_000, ExpiresAt: 1_700_000_900})
	parts := strings.Split(tok, ".")
	forgedHeader := `{"alg":"HS256","typ":"JWT","kid":"k1"}`
	forged := base64URL([]byte(forgedHeader)) + "." + parts[1] + "." + parts[2]
	_, err := e.verify(forged)
	if err == nil {
		t.Fatalf("expected alg-confusion rejection")
	}
}

func TestVerify_UnknownKID(t *testing.T) {
	e := newTestEngine(t)
	tok, _ := e.sign(Claims[testClaims]{Subject: "u", IssuedAt: 1_700_000_000, ExpiresAt: 1_700_000_900})
	parts := strings.Split(tok, ".")
	swapped := base64URL([]byte(`{"alg":"EdDSA","typ":"JWT","kid":"unknown"}`)) + "." + parts[1] + "." + parts[2]
	_, err := e.verify(swapped)
	if err == nil {
		t.Fatalf("expected unknown-kid rejection")
	}
}

func TestVerify_Expired(t *testing.T) {
	e := newTestEngine(t)
	tok, _ := e.sign(Claims[testClaims]{
		Subject: "u", IssuedAt: 1_699_999_000, ExpiresAt: 1_699_999_100,
	})
	_, err := e.verify(tok)
	assertErrCode(t, err, CodeExpiredToken)
}

func TestVerify_NotYetValid(t *testing.T) {
	e := newTestEngine(t)
	tok, _ := e.sign(Claims[testClaims]{
		Subject: "u", IssuedAt: 1_700_000_000, ExpiresAt: 1_700_010_000,
		NotBefore: 1_700_005_000,
	})
	_, err := e.verify(tok)
	assertErrCode(t, err, CodeInvalidToken)
}

func TestVerify_BadIssuer(t *testing.T) {
	e := newTestEngine(t)
	tok, _ := e.sign(Claims[testClaims]{
		Issuer: "evilapp", Subject: "u",
		IssuedAt: 1_700_000_000, ExpiresAt: 1_700_000_900,
	})
	_, err := e.verify(tok)
	assertErrCode(t, err, CodeInvalidToken)
}

func TestVerify_BadAudience(t *testing.T) {
	e := newTestEngine(t)
	tok, _ := e.sign(Claims[testClaims]{
		Subject: "u", Audience: []string{"mobile"},
		IssuedAt: 1_700_000_000, ExpiresAt: 1_700_000_900,
	})
	_, err := e.verify(tok)
	assertErrCode(t, err, CodeInvalidToken)
}

func TestVerify_LeewayBoundary(t *testing.T) {
	e := newTestEngine(t)
	// exp = now - 30s, leeway = 60s → still valid.
	tok, _ := e.sign(Claims[testClaims]{
		Subject: "u", IssuedAt: 1_699_999_900, ExpiresAt: 1_699_999_970,
	})
	if _, err := e.verify(tok); err != nil {
		t.Fatalf("expected token within leeway to verify; err = %v", err)
	}
}

func base64URL(b []byte) string {
	return strings.TrimRight(strings.ReplaceAll(strings.ReplaceAll(
		jwtBase64.EncodeToString(b), "+", "-"), "/", "_"), "=")
}

func assertErrCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %q, got nil", want)
	}
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("err is not *errs.Error: %v", err)
	}
	if e.Code != want {
		t.Fatalf("code = %q, want %q (full: %v)", e.Code, want, err)
	}
}
