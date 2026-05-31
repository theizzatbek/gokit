package auth

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// memKeyStore is a goroutine-safe in-memory KeyStore for tests. The
// stored record is keyed by hex(hash) so tests pre-compute the HMAC
// once and call back-to-back without seam races.
type memKeyStore struct {
	records map[string]*KeyRecord
	lookups int
}

func (s *memKeyStore) Lookup(_ context.Context, hash []byte) (*KeyRecord, error) {
	s.lookups++
	if r, ok := s.records[hex.EncodeToString(hash)]; ok {
		return r, nil
	}
	return nil, xerrs.NotFound(CodeAPIKeyInvalid, "no record")
}

// testAuth builds a minimal *Auth[map[string]any] suitable for the
// API-key middleware path (Bearer / refresh paths are NOT exercised).
func testAuth(t *testing.T, secret []byte) *Auth[map[string]any] {
	t.Helper()
	keySet, err := GenerateEd25519Key("kid1")
	if err != nil {
		t.Fatal(err)
	}
	a, err := New[map[string]any](Config{
		Issuer:           "test",
		Audience:         []string{"test"},
		Keys:             keySet,
		AccessTTL:        time.Minute,
		RefreshTTL:       time.Hour,
		APIKeyHashSecret: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAPIKey_HappyPath_PrincipalPopulated(t *testing.T) {
	secret := []byte("test-secret-32-bytes-________xx")
	a := testAuth(t, secret)
	store := &memKeyStore{records: map[string]*KeyRecord{}}
	hashHex := hex.EncodeToString(HashAPIKey("k-abc", secret))
	store.records[hashHex] = &KeyRecord{
		ID: "rec-1", Subject: "svc-a", Scopes: []string{"read"}, Role: "service",
	}

	app := fiber.New(fiber.Config{ErrorHandler: fiber.DefaultErrorHandler})
	app.Use(a.APIKey(store))
	app.Get("/me", func(c *fiber.Ctx) error {
		p, ok := From[map[string]any](c)
		if !ok {
			return c.Status(500).SendString("no principal")
		}
		return c.JSON(fiber.Map{
			"subject": p.Subject, "scopes": p.Scopes,
			"roles": p.Roles, "issuer": p.Issuer, "jti": p.JTI,
		})
	})
	req := httptest.NewRequest("GET", "/me", nil)
	req.Header.Set("X-API-Key", "k-abc")
	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body: %s", resp.StatusCode, body)
	}
	var got map[string]any
	body, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &got)
	if got["subject"] != "svc-a" {
		t.Errorf("subject = %v, want svc-a", got["subject"])
	}
	if got["issuer"] != "api_key" {
		t.Errorf("issuer = %v, want api_key", got["issuer"])
	}
	if got["jti"] != "rec-1" {
		t.Errorf("jti = %v, want rec-1", got["jti"])
	}
}

func TestAPIKey_MissingHeader_Required_401(t *testing.T) {
	secret := []byte("test-secret-32-bytes-________xx")
	a := testAuth(t, secret)
	store := &memKeyStore{records: map[string]*KeyRecord{}}

	app := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
		var e *xerrs.Error
		if errors.As(err, &e) {
			return c.Status(401).JSON(fiber.Map{"code": e.Code})
		}
		return c.Status(500).JSON(fiber.Map{"err": err.Error()})
	}})
	app.Use(a.APIKey(store))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })

	resp, _ := app.Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	var body map[string]any
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &body)
	if body["code"] != CodeAPIKeyMissing {
		t.Errorf("code = %v, want %s", body["code"], CodeAPIKeyMissing)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got == "" {
		t.Error("WWW-Authenticate header missing")
	}
}

func TestAPIKey_MissingHeader_Optional_PassThrough(t *testing.T) {
	secret := []byte("test-secret-32-bytes-________xx")
	a := testAuth(t, secret)
	store := &memKeyStore{records: map[string]*KeyRecord{}}

	app := fiber.New()
	app.Use(a.APIKey(store, WithAPIKeyOptional()))
	app.Get("/", func(c *fiber.Ctx) error {
		_, hasPrincipal := From[map[string]any](c)
		return c.JSON(fiber.Map{"principal": hasPrincipal})
	})
	resp, _ := app.Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]bool
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &body)
	if body["principal"] {
		t.Errorf("principal should be absent for anonymous + optional, got %v", body)
	}
}

func TestAPIKey_UnknownKey_401Invalid(t *testing.T) {
	secret := []byte("test-secret-32-bytes-________xx")
	a := testAuth(t, secret)
	store := &memKeyStore{records: map[string]*KeyRecord{}}

	app := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
		var e *xerrs.Error
		if errors.As(err, &e) {
			return c.Status(401).JSON(fiber.Map{"code": e.Code})
		}
		return c.Status(500).SendString(err.Error())
	}})
	app.Use(a.APIKey(store))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "ghost-key")
	resp, _ := app.Test(req)
	var body map[string]any
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &body)
	if body["code"] != CodeAPIKeyInvalid {
		t.Errorf("code = %v, want %s (existence side channel suppressed)", body["code"], CodeAPIKeyInvalid)
	}
}

func TestAPIKey_Expired_401(t *testing.T) {
	secret := []byte("test-secret-32-bytes-________xx")
	a := testAuth(t, secret)
	store := &memKeyStore{records: map[string]*KeyRecord{}}
	hashHex := hex.EncodeToString(HashAPIKey("expired-key", secret))
	store.records[hashHex] = &KeyRecord{
		Subject: "svc-x", ExpiresAt: time.Now().Add(-1 * time.Minute),
	}

	app := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
		var e *xerrs.Error
		if errors.As(err, &e) {
			return c.Status(401).JSON(fiber.Map{"code": e.Code})
		}
		return c.Status(500).SendString(err.Error())
	}})
	app.Use(a.APIKey(store))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "expired-key")
	resp, _ := app.Test(req)
	var body map[string]any
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &body)
	if body["code"] != CodeAPIKeyExpired {
		t.Errorf("code = %v, want %s", body["code"], CodeAPIKeyExpired)
	}
}

func TestAPIKey_Revoked_401(t *testing.T) {
	secret := []byte("test-secret-32-bytes-________xx")
	a := testAuth(t, secret)
	store := &memKeyStore{records: map[string]*KeyRecord{}}
	hashHex := hex.EncodeToString(HashAPIKey("revoked-key", secret))
	store.records[hashHex] = &KeyRecord{
		Subject: "svc-x", RevokedAt: time.Now().Add(-1 * time.Hour),
	}

	app := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
		var e *xerrs.Error
		if errors.As(err, &e) {
			return c.Status(401).JSON(fiber.Map{"code": e.Code})
		}
		return c.Status(500).SendString(err.Error())
	}})
	app.Use(a.APIKey(store))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "revoked-key")
	resp, _ := app.Test(req)
	var body map[string]any
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &body)
	if body["code"] != CodeAPIKeyRevoked {
		t.Errorf("code = %v, want %s", body["code"], CodeAPIKeyRevoked)
	}
}

func TestAPIKey_MissingSecret_PanicsAtBuild(t *testing.T) {
	a := testAuth(t, nil) // no APIKeyHashSecret
	store := &memKeyStore{records: map[string]*KeyRecord{}}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on missing secret")
		}
		var e *xerrs.Error
		if !errors.As(r.(error), &e) || e.Code != CodeAPIKeyMissingSecret {
			t.Errorf("recovered = %v, want CodeAPIKeyMissingSecret", r)
		}
	}()
	_ = a.APIKey(store)
}

func TestAPIKey_QueryFallback(t *testing.T) {
	secret := []byte("test-secret-32-bytes-________xx")
	a := testAuth(t, secret)
	store := &memKeyStore{records: map[string]*KeyRecord{}}
	hashHex := hex.EncodeToString(HashAPIKey("query-key", secret))
	store.records[hashHex] = &KeyRecord{Subject: "svc-q"}

	app := fiber.New()
	app.Use(a.APIKey(store, WithAPIKeyQuery("api_key")))
	app.Get("/", func(c *fiber.Ctx) error {
		p, _ := From[map[string]any](c)
		return c.SendString(p.Subject)
	})
	resp, _ := app.Test(httptest.NewRequest("GET", "/?api_key=query-key", nil))
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "svc-q" {
		t.Errorf("body = %s, want svc-q (query-fallback didn't fire)", body)
	}
}

func TestAPIKeyFactory_OptionalArg(t *testing.T) {
	secret := []byte("test-secret-32-bytes-________xx")
	a := testAuth(t, secret)
	store := &memKeyStore{records: map[string]*KeyRecord{}}

	factory := a.APIKeyFactory(store)
	if _, err := factory([]any{}); err != nil {
		t.Errorf("factory([]) = %v, want nil", err)
	}
	if _, err := factory([]any{"optional"}); err != nil {
		t.Errorf("factory([optional]) = %v, want nil", err)
	}
	if _, err := factory([]any{"unknown"}); err == nil {
		t.Errorf("factory([unknown]) should error")
	}
}
