package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

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
