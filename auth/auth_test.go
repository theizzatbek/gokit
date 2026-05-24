package auth

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/theizzatbek/fibermap/errs"
)

func mustNewAuth(t *testing.T) *Auth[testClaims] {
	t.Helper()
	ks, _ := GenerateEd25519Key("k1")
	a, err := New[testClaims](Config{
		Issuer: "myapp", Audience: []string{"web"},
		Keys: ks, AccessTTL: 15 * time.Minute, RefreshTTL: 30 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func TestNew_RejectsMissingKeys(t *testing.T) {
	_, err := New[testClaims](Config{Issuer: "x", AccessTTL: time.Minute, RefreshTTL: time.Hour})
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindValidation {
		t.Fatalf("expected Validation err, got %v", err)
	}
}

func TestNew_RejectsZeroTTL(t *testing.T) {
	ks, _ := GenerateEd25519Key("k1")
	_, err := New[testClaims](Config{Keys: ks, AccessTTL: 0, RefreshTTL: time.Hour})
	if err == nil {
		t.Fatalf("expected Validation err for AccessTTL=0")
	}
}

func TestSignVerify_RoundTripViaAuth(t *testing.T) {
	a := mustNewAuth(t)
	tok, err := a.Sign(Claims[testClaims]{
		Subject:   "u-1",
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
		IssuedAt:  time.Now().Unix(),
		Custom:    testClaims{TenantID: "t-9"},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, err := a.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Subject != "u-1" || got.Custom.TenantID != "t-9" {
		t.Fatalf("Claims = %#v", got)
	}
}

func TestOptions_DefaultLeewayIsOneMinute(t *testing.T) {
	a := mustNewAuth(t)
	if a.eng.cfg.Leeway != time.Minute {
		t.Fatalf("default Leeway = %v, want 1m", a.eng.cfg.Leeway)
	}
}

func TestWithLogger_StoresLogger(t *testing.T) {
	ks, _ := GenerateEd25519Key("k1")
	log := slog.New(slog.DiscardHandler)
	a, _ := New[testClaims](
		Config{Keys: ks, AccessTTL: time.Minute, RefreshTTL: time.Hour},
		WithLogger(log),
	)
	if a.logger != log {
		t.Fatalf("logger not stored")
	}
}
