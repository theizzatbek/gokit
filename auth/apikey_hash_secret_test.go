package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"testing"
	"time"
)

// Tests for v1.1.0 P1-9: auth.WithAPIKeyHashSecret Option. The secret
// previously had only one input path (Config.APIKeyHashSecret), which
// surfaced as a footgun whenever a caller built Auth with the With*
// family for everything else and then forgot the one field-set call.

func mustNewAuthForHashSecret(t *testing.T, cfg Config, opts ...Option) *Auth[map[string]any] {
	t.Helper()
	keySet, err := GenerateEd25519Key("kid1")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Issuer = "test"
	cfg.Audience = []string{"test"}
	cfg.Keys = keySet
	cfg.AccessTTL = time.Minute
	cfg.RefreshTTL = time.Hour
	a, err := New[map[string]any](cfg, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// computeExpectedHash mirrors HashAPIKey's HMAC-SHA256 contract so
// tests can prove the constructed Auth actually USES the configured
// secret end-to-end (vs storing it but using something else).
func computeExpectedHash(secret []byte, plain string) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(plain))
	return mac.Sum(nil)
}

func TestWithAPIKeyHashSecret_OnlyOption_PopulatesSecret(t *testing.T) {
	secret := []byte("option-supplied-secret-32-bytes_")
	a := mustNewAuthForHashSecret(t, Config{}, WithAPIKeyHashSecret(secret))

	got := HashAPIKey("k-abc", a.apiKeyHashSecret)
	want := computeExpectedHash(secret, "k-abc")
	if !hmac.Equal(got, want) {
		t.Errorf("HashAPIKey('k-abc', Auth.secret) = %x, want %x", got, want)
	}
}

func TestWithAPIKeyHashSecret_BothSet_OptionWinsOverConfig(t *testing.T) {
	configSecret := []byte("config-supplied-secret-32-bytes_")
	optionSecret := []byte("option-supplied-secret-32-bytes_")
	a := mustNewAuthForHashSecret(t,
		Config{APIKeyHashSecret: configSecret},
		WithAPIKeyHashSecret(optionSecret),
	)

	got := HashAPIKey("k-abc", a.apiKeyHashSecret)
	wantOption := computeExpectedHash(optionSecret, "k-abc")
	wantConfig := computeExpectedHash(configSecret, "k-abc")
	if !hmac.Equal(got, wantOption) {
		t.Errorf("Auth.secret resolved to config-derived %x; expected option-derived %x", wantConfig, wantOption)
	}
}

func TestWithAPIKeyHashSecret_EmptyOption_DefersToConfig(t *testing.T) {
	configSecret := []byte("config-supplied-secret-32-bytes_")
	a := mustNewAuthForHashSecret(t,
		Config{APIKeyHashSecret: configSecret},
		WithAPIKeyHashSecret(nil), // explicit nil override → fall through to Config
	)

	got := HashAPIKey("k-abc", a.apiKeyHashSecret)
	want := computeExpectedHash(configSecret, "k-abc")
	if !hmac.Equal(got, want) {
		t.Errorf("nil-Option override leaked: got %x, want config-derived %x", got, want)
	}
}

func TestWithAPIKeyHashSecret_NeitherSet_AuthBuildsButPanicsOnMiddleware(t *testing.T) {
	// Auth.New does NOT enforce the floor — that surfaces only when
	// APIKey middleware is requested. This test guards the boundary:
	// the constructor succeeds, the secret slot is empty, and the
	// existing CodeAPIKeyMissingSecret panic still fires when
	// APIKey() is called downstream (already covered by
	// TestAPIKey_MissingSecret_PanicsAtBuild in apikey_test.go; we
	// just verify the Auth value is in the documented "empty secret"
	// state).
	a := mustNewAuthForHashSecret(t, Config{})
	if len(a.apiKeyHashSecret) != 0 {
		t.Errorf("expected empty apiKeyHashSecret, got %d bytes", len(a.apiKeyHashSecret))
	}
}
