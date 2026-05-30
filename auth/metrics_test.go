package auth_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/auth/internal/memstore"
)

// newTestAuth wires an *auth.Auth[map[string]any] with a memstore
// backend and the given metrics registry.
func newTestAuth(t *testing.T, reg prometheus.Registerer) *auth.Auth[map[string]any] {
	t.Helper()
	keys, err := auth.GenerateEd25519Key("k1")
	if err != nil {
		t.Fatalf("GenerateEd25519Key: %v", err)
	}
	store := memstore.New()
	a, err := auth.New[map[string]any](auth.Config{
		Issuer: "test", Keys: keys,
		AccessTTL: time.Minute, RefreshTTL: time.Hour,
	}, auth.WithRefreshStore(store), auth.WithMetrics(reg))
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	return a
}

func counterValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.Metric {
			if !labelsMatch(m, labels) {
				continue
			}
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

func labelsMatch(m *dto.Metric, want map[string]string) bool {
	got := map[string]string{}
	for _, l := range m.GetLabel() {
		got[l.GetName()] = l.GetValue()
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

func TestMetrics_IssueLogin_IncrementsCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	a := newTestAuth(t, reg)

	_, err := a.IssueTokens(context.Background(),
		auth.LoginResult[map[string]any]{Subject: "u1"}, auth.IssueMeta{})
	if err != nil {
		t.Fatalf("IssueTokens: %v", err)
	}

	if got := counterValue(t, reg, "auth_tokens_issued_total", map[string]string{"op": "login"}); got != 1 {
		t.Errorf("auth_tokens_issued_total{op=login} = %v, want 1", got)
	}
}

func TestMetrics_Bearer_InvalidIncrements(t *testing.T) {
	reg := prometheus.NewRegistry()
	a := newTestAuth(t, reg)

	app := fiber.New()
	app.Use(a.Bearer(auth.BearerRequired))
	app.Get("/ping", func(c *fiber.Ctx) error { return c.SendString("ok") })

	req := httptest.NewRequest("GET", "/ping", nil)
	req.Header.Set("Authorization", "Bearer not-a-valid-jwt")
	if _, err := app.Test(req); err != nil {
		t.Fatalf("app.Test: %v", err)
	}

	if got := counterValue(t, reg, "auth_bearer_verify_total", map[string]string{"outcome": "invalid"}); got != 1 {
		t.Errorf("auth_bearer_verify_total{outcome=invalid} = %v, want 1", got)
	}
}

func TestMetrics_RateLimit_DeniedCounted(t *testing.T) {
	reg := prometheus.NewRegistry()
	a := newTestAuth(t, reg)

	app := fiber.New()
	app.Use(a.RateLimit(0.001, 1)) // burst 1, ~0 rps refill — second hit denies
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendString("ok") })

	for range 3 {
		if _, err := app.Test(httptest.NewRequest("GET", "/x", nil)); err != nil {
			t.Fatalf("app.Test: %v", err)
		}
	}
	got := counterValue(t, reg, "auth_ratelimit_denied_total", nil)
	if got < 1 {
		t.Errorf("auth_ratelimit_denied_total = %v, want >= 1", got)
	}
}

func TestMetrics_Idempotency_HitMissSkip(t *testing.T) {
	reg := prometheus.NewRegistry()
	a := newTestAuth(t, reg)

	app := fiber.New()
	app.Use(a.Idempotency(time.Minute))
	calls := 0
	app.Post("/orders", func(c *fiber.Ctx) error {
		calls++
		return c.Status(201).SendString("created")
	})
	app.Get("/orders", func(c *fiber.Ctx) error { return c.SendString("list") })

	// skip — GET is a safe method
	if _, err := app.Test(httptest.NewRequest("GET", "/orders", nil)); err != nil {
		t.Fatal(err)
	}
	// miss — first POST stores
	r1 := httptest.NewRequest("POST", "/orders", strings.NewReader(""))
	r1.Header.Set(auth.IdempotencyHeader, "abc")
	if _, err := app.Test(r1); err != nil {
		t.Fatal(err)
	}
	// hit — second POST replays
	r2 := httptest.NewRequest("POST", "/orders", strings.NewReader(""))
	r2.Header.Set(auth.IdempotencyHeader, "abc")
	if _, err := app.Test(r2); err != nil {
		t.Fatal(err)
	}

	if got := counterValue(t, reg, "auth_idempotency_total", map[string]string{"outcome": "skip"}); got != 1 {
		t.Errorf("idempotency{outcome=skip} = %v, want 1", got)
	}
	if got := counterValue(t, reg, "auth_idempotency_total", map[string]string{"outcome": "miss"}); got != 1 {
		t.Errorf("idempotency{outcome=miss} = %v, want 1", got)
	}
	if got := counterValue(t, reg, "auth_idempotency_total", map[string]string{"outcome": "hit"}); got != 1 {
		t.Errorf("idempotency{outcome=hit} = %v, want 1", got)
	}
}

func TestMetrics_NilSafe_NoWithMetrics(t *testing.T) {
	// Sanity: without WithMetrics every increment helper is a no-op
	// — no panic, no nil deref. The Auth.metrics field is unexported
	// so we only check externally observable behaviour.
	keys, _ := auth.GenerateEd25519Key("k1")
	a, err := auth.New[map[string]any](auth.Config{
		Issuer: "test", Keys: keys,
		AccessTTL: time.Minute, RefreshTTL: time.Hour,
	}, auth.WithRefreshStore(memstore.New()))
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	if _, err := a.IssueTokens(context.Background(),
		auth.LoginResult[map[string]any]{Subject: "u1"}, auth.IssueMeta{}); err != nil {
		t.Errorf("IssueTokens with nil metrics panicked or failed: %v", err)
	}
}
