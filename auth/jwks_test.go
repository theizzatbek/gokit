package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestKeySet_JWKS_EdDSA(t *testing.T) {
	ks, err := GenerateEd25519Key("k-ed")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ks.JWKS()
	if err != nil {
		t.Fatalf("JWKS: %v", err)
	}
	var out JWKS
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Keys) != 1 {
		t.Fatalf("len(keys) = %d, want 1", len(out.Keys))
	}
	k := out.Keys[0]
	if k.KID != "k-ed" || k.Kty != "OKP" || k.Crv != "Ed25519" || k.Alg != "EdDSA" || k.Use != "sig" {
		t.Errorf("JWK = %+v", k)
	}
	if k.X == "" {
		t.Error("X is empty")
	}
	if k.Y != "" {
		t.Errorf("Y must be empty for Ed25519, got %q", k.Y)
	}
}

func TestKeySet_JWKS_ES256(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	entry := signingKey{KID: "k-ec", Alg: "ES256", Priv: priv, Pub: &priv.PublicKey}
	ks := &KeySet{active: entry, verify: map[string]signingKey{"k-ec": entry}}

	raw, err := ks.JWKS()
	if err != nil {
		t.Fatalf("JWKS: %v", err)
	}
	var out JWKS
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	k := out.Keys[0]
	if k.Kty != "EC" || k.Crv != "P-256" || k.Alg != "ES256" {
		t.Errorf("JWK = %+v", k)
	}
	if k.X == "" || k.Y == "" {
		t.Error("X or Y empty")
	}
}

func TestAuth_JWKSHandler_ServesAndCaches(t *testing.T) {
	a := mustNewAuth(t)

	app := fiber.New()
	app.Get("/jwks.json", a.JWKSHandler(120))

	req := httptest.NewRequest("GET", "/jwks.json", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=120" {
		t.Errorf("Cache-Control = %q", cc)
	}
}

func TestAuth_JWKSHandler_MaxAgeZeroIsNoStore(t *testing.T) {
	a := mustNewAuth(t)
	app := fiber.New()
	app.Get("/jwks.json", a.JWKSHandler(0))

	resp, _ := app.Test(httptest.NewRequest("GET", "/jwks.json", nil), -1)
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

func TestAuth_RotateKeys_VerifyUsesNewSet(t *testing.T) {
	a := mustNewAuth(t)
	old, err := a.Sign(Claims[testClaims]{
		Subject:   "u",
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
		IssuedAt:  time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Rotate to a brand new keypair (no overlap).
	ks2, _ := GenerateEd25519Key("k2")
	if err := a.RotateKeys(ks2); err != nil {
		t.Fatalf("RotateKeys: %v", err)
	}

	// Old token signed with k1 must now FAIL because k1 is no
	// longer in the verify set after the swap.
	if _, err := a.Verify(old); err == nil {
		t.Fatal("Verify of old token must fail after rotation to fresh KeySet")
	}

	// A token signed AFTER the rotation must verify cleanly.
	fresh, err := a.Sign(Claims[testClaims]{
		Subject:   "u",
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
		IssuedAt:  time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Sign after rotate: %v", err)
	}
	if _, err := a.Verify(fresh); err != nil {
		t.Errorf("Verify of fresh token: %v", err)
	}
}

func TestAuth_RotateKeys_RejectsNil(t *testing.T) {
	a := mustNewAuth(t)
	if err := a.RotateKeys(nil); err == nil {
		t.Error("expected error on nil KeySet")
	}
}
