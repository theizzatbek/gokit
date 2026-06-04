package auth_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/auth"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// inMemKeyStore is a goroutine-safe stub for the improvements tests.
// The internal apikey_test.go declares one too in package `auth`; we
// re-declare here so the auth_test package stays self-contained.
type inMemKeyStore struct {
	records map[string]*auth.KeyRecord
	lookups int
	failErr error
}

func (s *inMemKeyStore) Lookup(_ context.Context, hash []byte) (*auth.KeyRecord, error) {
	s.lookups++
	if s.failErr != nil {
		return nil, s.failErr
	}
	if r, ok := s.records[hex.EncodeToString(hash)]; ok {
		return r, nil
	}
	return nil, xerrs.NotFound(auth.CodeAPIKeyInvalid, "no record")
}

// apikeyAuth builds an *auth.Auth[map[string]any] with the given
// option list applied. Always wires APIKeyHashSecret so the middleware
// build never panics in these tests.
func apikeyAuth(t *testing.T, opts ...auth.Option) *auth.Auth[map[string]any] {
	t.Helper()
	keys, err := auth.GenerateEd25519Key("k1")
	if err != nil {
		t.Fatalf("GenerateEd25519Key: %v", err)
	}
	a, err := auth.New[map[string]any](auth.Config{
		Issuer: "test", Keys: keys,
		AccessTTL: time.Minute, RefreshTTL: time.Hour,
		APIKeyHashSecret: []byte("test-secret-32-bytes-________xx"),
	}, opts...)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	return a
}

func TestAPIKey_Metrics_SuccessIncrements(t *testing.T) {
	reg := prometheus.NewRegistry()
	a := apikeyAuth(t, auth.WithMetrics(reg))
	secret := []byte("test-secret-32-bytes-________xx")
	store := &inMemKeyStore{records: map[string]*auth.KeyRecord{}}
	store.records[hex.EncodeToString(auth.HashAPIKey("ok", secret))] = &auth.KeyRecord{
		ID: "k1", Subject: "svc",
	}

	app := fiber.New()
	app.Use(a.APIKey(store))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "ok")
	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	if got := counterValue(t, reg, "auth_apikey_authentications_total",
		map[string]string{"outcome": "success"}); got != 1 {
		t.Errorf("success counter = %v, want 1", got)
	}
}

func TestAPIKey_Metrics_OutcomesBreakdown(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(s *inMemKeyStore, secret []byte)
		header  string
		outcome string
	}{
		{
			name:    "missing",
			setup:   func(*inMemKeyStore, []byte) {},
			header:  "",
			outcome: "missing",
		},
		{
			name:    "invalid",
			setup:   func(*inMemKeyStore, []byte) {},
			header:  "no-such",
			outcome: "invalid",
		},
		{
			name: "expired",
			setup: func(s *inMemKeyStore, secret []byte) {
				s.records[hex.EncodeToString(auth.HashAPIKey("k", secret))] = &auth.KeyRecord{
					Subject: "svc", ExpiresAt: time.Now().Add(-1 * time.Hour),
				}
			},
			header:  "k",
			outcome: "expired",
		},
		{
			name: "revoked",
			setup: func(s *inMemKeyStore, secret []byte) {
				s.records[hex.EncodeToString(auth.HashAPIKey("k", secret))] = &auth.KeyRecord{
					Subject: "svc", RevokedAt: time.Now().Add(-1 * time.Hour),
				}
			},
			header:  "k",
			outcome: "revoked",
		},
		{
			name: "error",
			setup: func(s *inMemKeyStore, _ []byte) {
				s.failErr = xerrs.Internal("backend_down", "lookup blew up")
			},
			header:  "x",
			outcome: "error",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			a := apikeyAuth(t, auth.WithMetrics(reg))
			secret := []byte("test-secret-32-bytes-________xx")
			store := &inMemKeyStore{records: map[string]*auth.KeyRecord{}}
			tc.setup(store, secret)

			app := fiber.New(fiber.Config{
				ErrorHandler: func(c *fiber.Ctx, err error) error {
					return c.Status(500).SendString(err.Error())
				},
			})
			app.Use(a.APIKey(store))
			app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })
			req := httptest.NewRequest("GET", "/", nil)
			if tc.header != "" {
				req.Header.Set("X-API-Key", tc.header)
			}
			if _, err := app.Test(req); err != nil {
				t.Fatalf("Test: %v", err)
			}
			if got := counterValue(t, reg, "auth_apikey_authentications_total",
				map[string]string{"outcome": tc.outcome}); got != 1 {
				t.Errorf("counter[%s] = %v, want 1", tc.outcome, got)
			}
		})
	}
}

func TestAPIKey_LookupDuration_Histogram(t *testing.T) {
	reg := prometheus.NewRegistry()
	a := apikeyAuth(t, auth.WithMetrics(reg))
	secret := []byte("test-secret-32-bytes-________xx")
	store := &inMemKeyStore{records: map[string]*auth.KeyRecord{}}
	store.records[hex.EncodeToString(auth.HashAPIKey("k", secret))] = &auth.KeyRecord{Subject: "s"}

	app := fiber.New()
	app.Use(a.APIKey(store))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "k")
	if _, err := app.Test(req); err != nil {
		t.Fatalf("Test: %v", err)
	}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "auth_apikey_lookup_duration_seconds" {
			found = true
			if len(mf.Metric) != 1 {
				t.Errorf("histogram metrics = %d, want 1", len(mf.Metric))
				continue
			}
			if got := mf.Metric[0].GetHistogram().GetSampleCount(); got != 1 {
				t.Errorf("sample_count = %d, want 1", got)
			}
		}
	}
	if !found {
		t.Error("auth_apikey_lookup_duration_seconds not registered")
	}
}

func TestAPIKey_OnSuccessHook_FiresWithPrincipal(t *testing.T) {
	var (
		gotSubject string
		gotJTI     string
		gotScopes  []string
		gotRoles   []string
		fires      int32
	)
	hook := func(_ *fiber.Ctx, subject, jti string, scopes, roles []string) {
		atomic.AddInt32(&fires, 1)
		gotSubject = subject
		gotJTI = jti
		gotScopes = scopes
		gotRoles = roles
	}

	a := apikeyAuth(t)
	secret := []byte("test-secret-32-bytes-________xx")
	store := &inMemKeyStore{records: map[string]*auth.KeyRecord{}}
	store.records[hex.EncodeToString(auth.HashAPIKey("ok", secret))] = &auth.KeyRecord{
		ID: "rec-1", Subject: "svc-a", Scopes: []string{"read"}, Role: "user",
	}

	app := fiber.New()
	app.Use(a.APIKey(store, auth.WithAPIKeyOnSuccess(hook)))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "ok")
	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if atomic.LoadInt32(&fires) != 1 {
		t.Errorf("hook fires = %d, want 1", fires)
	}
	if gotSubject != "svc-a" || gotJTI != "rec-1" {
		t.Errorf("subject=%q jti=%q, want svc-a/rec-1", gotSubject, gotJTI)
	}
	if len(gotScopes) != 1 || gotScopes[0] != "read" {
		t.Errorf("scopes = %v, want [read]", gotScopes)
	}
	if len(gotRoles) != 1 || gotRoles[0] != "user" {
		t.Errorf("roles = %v, want [user]", gotRoles)
	}
}

func TestAPIKey_OnFailHook_FiresWithCode(t *testing.T) {
	codes := make([]string, 0, 4)
	hook := func(_ *fiber.Ctx, code string) { codes = append(codes, code) }
	a := apikeyAuth(t)
	store := &inMemKeyStore{records: map[string]*auth.KeyRecord{}}

	app := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, _ error) error {
		return c.Status(401).SendString("nope")
	}})
	app.Use(a.APIKey(store, auth.WithAPIKeyOnFail(hook)))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })

	// missing
	if _, err := app.Test(httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Fatal(err)
	}
	// invalid
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "ghost")
	if _, err := app.Test(req); err != nil {
		t.Fatal(err)
	}
	if len(codes) != 2 {
		t.Fatalf("hook fires = %d, want 2 (codes=%v)", len(codes), codes)
	}
	if codes[0] != auth.CodeAPIKeyMissing {
		t.Errorf("codes[0] = %q, want %s", codes[0], auth.CodeAPIKeyMissing)
	}
	if codes[1] != auth.CodeAPIKeyInvalid {
		t.Errorf("codes[1] = %q, want %s", codes[1], auth.CodeAPIKeyInvalid)
	}
}

func TestAPIKey_Hooks_PanicRecovered(t *testing.T) {
	logbuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logbuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a := apikeyAuth(t, auth.WithLogger(logger))
	secret := []byte("test-secret-32-bytes-________xx")
	store := &inMemKeyStore{records: map[string]*auth.KeyRecord{}}
	store.records[hex.EncodeToString(auth.HashAPIKey("k", secret))] = &auth.KeyRecord{Subject: "s"}

	app := fiber.New()
	app.Use(a.APIKey(store,
		auth.WithAPIKeyOnSuccess(func(*fiber.Ctx, string, string, []string, []string) {
			panic("success hook boom")
		}),
	))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "k")
	resp, _ := app.Test(req)
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 (hook panic should not break request)", resp.StatusCode)
	}
	if !bytes.Contains(logbuf.Bytes(), []byte("OnSuccess panic recovered")) {
		t.Errorf("logger did not record panic; logs=%q", logbuf.String())
	}
}

func TestAPIKey_SecurityLogger_EmitsOnSuccessAndFail(t *testing.T) {
	secbuf := &bytes.Buffer{}
	seclog := slog.New(slog.NewTextHandler(secbuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	a := apikeyAuth(t, auth.WithSecurityLogger(seclog))
	secret := []byte("test-secret-32-bytes-________xx")
	store := &inMemKeyStore{records: map[string]*auth.KeyRecord{}}
	store.records[hex.EncodeToString(auth.HashAPIKey("ok", secret))] = &auth.KeyRecord{Subject: "svc"}

	app := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, _ error) error {
		return c.Status(401).SendString("nope")
	}})
	app.Use(a.APIKey(store))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })

	// success
	okReq := httptest.NewRequest("GET", "/", nil)
	okReq.Header.Set("X-API-Key", "ok")
	if _, err := app.Test(okReq); err != nil {
		t.Fatal(err)
	}
	// missing
	if _, err := app.Test(httptest.NewRequest("GET", "/", nil)); err != nil {
		t.Fatal(err)
	}

	logs := secbuf.String()
	if !bytes.Contains([]byte(logs), []byte("apikey_auth_success")) {
		t.Errorf("missing apikey_auth_success in security log:\n%s", logs)
	}
	if !bytes.Contains([]byte(logs), []byte("apikey_missing")) {
		t.Errorf("missing apikey_missing in security log:\n%s", logs)
	}
}

func TestAPIKey_LookupError_PassesThrough(t *testing.T) {
	a := apikeyAuth(t)
	store := &inMemKeyStore{
		records: map[string]*auth.KeyRecord{},
		failErr: xerrs.Internal("backend", "boom"),
	}
	captured := make(chan error, 1)
	app := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
		captured <- err
		return c.Status(503).SendString("backend")
	}})
	app.Use(a.APIKey(store))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "k")
	resp, _ := app.Test(req)
	if resp.StatusCode != 503 {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	var e *xerrs.Error
	select {
	case got := <-captured:
		if !errors.As(got, &e) || e.Kind != xerrs.KindInternal {
			t.Errorf("err = %v, want KindInternal", got)
		}
	default:
		t.Fatal("ErrorHandler did not receive an error")
	}
	_, _ = io.ReadAll(resp.Body)
}
