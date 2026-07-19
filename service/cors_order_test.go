package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/fibermap"
)

// These tests pin the app-level middleware ORDER that Run installs via
// appLevelMiddleware: CORS must run BEFORE the Bearer(BearerOptional)
// layer so cross-origin browsers can read auth 401s (expired/broken
// token → 401 must still carry Access-Control-Allow-Origin, otherwise
// the typical "caught 401 → go refresh" front-end flow dies at the
// browser CORS wall). The bearer layer itself must stay BEFORE the
// engine's contextInit so ContextBuilder still sees the principal.

// nopRefreshStore satisfies auth.RefreshStore for token minting in
// tests that never touch refresh flows.
type nopRefreshStore struct{}

func (nopRefreshStore) Issue(context.Context, auth.Record) error { return nil }
func (nopRefreshStore) Consume(context.Context, [32]byte, time.Time) (auth.Record, error) {
	return auth.Record{}, nil
}
func (nopRefreshStore) RevokeFamily(context.Context, string) error  { return nil }
func (nopRefreshStore) RevokeSubject(context.Context, string) error { return nil }
func (nopRefreshStore) GarbageCollect(context.Context, time.Time) (int64, error) {
	return 0, nil
}

// newCORSOrderService builds a DB-less *Service literal with a real
// Auth (nop refresh store, so IssueTokens works without Postgres) and
// the supplied options applied. service.New requires DB alongside Auth
// (refreshpg needs a Querier), which these middleware-order tests
// don't need — the literal mirrors what Run consumes: opts, logger,
// Auth, Engine.
func newCORSOrderService(t *testing.T, opts ...Option) *Service[smokeAppCtx, smokeClaims] {
	t.Helper()
	pemKey := smokeEd25519PEM(t)
	keys, err := auth.LoadKeysFromPEM("k1", map[string][]byte{"k1": []byte(pemKey)})
	if err != nil {
		t.Fatalf("LoadKeysFromPEM: %v", err)
	}
	a, err := auth.New[smokeClaims](auth.Config{
		Issuer:     "cors-test",
		Keys:       keys,
		AccessTTL:  15 * time.Minute,
		RefreshTTL: time.Hour,
	}, auth.WithRefreshStore(nopRefreshStore{}))
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}
	return &Service[smokeAppCtx, smokeClaims]{
		Auth:   a,
		Engine: fibermap.Default[smokeAppCtx](),
		opts:   o,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// corsOrderApp assembles a fiber.App the same way Run does: ErrorHandler
// from buildFiberConfig + the appLevelMiddleware chain via app.Use.
func corsOrderApp(t *testing.T, svc *Service[smokeAppCtx, smokeClaims]) *fiber.App {
	t.Helper()
	app := fiber.New(svc.buildFiberConfig())
	for _, mw := range svc.appLevelMiddleware() {
		app.Use(mw)
	}
	return app
}

// mintCORSAccess signs a valid access token via the service's own Auth
// (its nop refresh store makes IssueTokens DB-free).
func mintCORSAccess(t *testing.T, svc *Service[smokeAppCtx, smokeClaims], subject string) string {
	t.Helper()
	pair, err := svc.Auth.IssueTokens(context.Background(), auth.LoginResult[smokeClaims]{Subject: subject}, auth.IssueMeta{})
	if err != nil {
		t.Fatalf("IssueTokens: %v", err)
	}
	return pair.Access
}

const corsTestOrigin = "https://app.example.com"

// Chain-order guard: otelfiber is the OUTERMOST app-level middleware
// (so bearer 401s and CORS preflights land in traces), CORS comes
// right after it — before SecurityHeaders and the bearer layer.
func TestCORSOrder_ChainStartsWithOtelThenCORS(t *testing.T) {
	svc := newCORSOrderService(t, WithCORS(corsTestOrigin))
	svc.opts.otelServiceName = "cors-order-test"
	if err := svc.setupOtel(context.Background()); err != nil {
		t.Fatalf("setupOtel: %v", err)
	}
	t.Cleanup(func() {
		if svc.otelShutdown != nil {
			_ = svc.otelShutdown(context.Background())
		}
		if svc.otelMetricsShutdown != nil {
			_ = svc.otelMetricsShutdown(context.Background())
		}
		if svc.otelLogsShutdown != nil {
			_ = svc.otelLogsShutdown(context.Background())
		}
	})

	if svc.opts.otelFiberHandler == nil {
		t.Fatal("setupOtel must fill the otelFiberHandler slot")
	}
	if svc.opts.corsHandler == nil {
		t.Fatal("WithCORS must fill the corsHandler slot")
	}

	chain := svc.appLevelMiddleware()
	if len(chain) < 2 {
		t.Fatalf("chain too short: %d", len(chain))
	}
	if !sameHandler(chain[0], svc.opts.otelFiberHandler) {
		t.Error("chain[0] must be otelfiber (outermost)")
	}
	if !sameHandler(chain[1], svc.opts.corsHandler) {
		t.Error("chain[1] must be CORS (before SecurityHeaders / bearer)")
	}
}

func sameHandler(a, b fiber.Handler) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// Acceptance 1: a present-but-invalid Bearer token still gets rejected
// with 401, but the response must carry CORS headers — the CORS layer
// runs before the bearer layer.
func TestCORSOrder_InvalidBearer_401CarriesCORSHeaders(t *testing.T) {
	svc := newCORSOrderService(t, WithCORS(corsTestOrigin))
	app := corsOrderApp(t, svc)
	app.Get("/ping", func(c *fiber.Ctx) error { return c.SendString("pong") })

	req := httptest.NewRequest("GET", "/ping", nil)
	req.Header.Set("Origin", corsTestOrigin)
	req.Header.Set("Authorization", "Bearer garbage-token")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Fatalf("status = %d, want 401 (invalid token must still reject)", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != corsTestOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q — browser cannot read this 401 without it", got, corsTestOrigin)
	}
}

// Acceptance 3: preflight OPTIONS short-circuits in CORS BEFORE the
// bearer layer — even a garbage Authorization header (which browsers
// never send on preflight, but proxies might replay) must not turn the
// preflight into a 401.
func TestCORSOrder_Preflight_ShortCircuitsBeforeBearer(t *testing.T) {
	svc := newCORSOrderService(t, WithCORS(corsTestOrigin))
	app := corsOrderApp(t, svc)
	app.Get("/ping", func(c *fiber.Ctx) error { return c.SendString("pong") })

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"no token", ""},
		{"garbage token", "Bearer garbage-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("OPTIONS", "/ping", nil)
			req.Header.Set("Origin", corsTestOrigin)
			req.Header.Set("Access-Control-Request-Method", "GET")
			if tc.token != "" {
				req.Header.Set("Authorization", tc.token)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != 204 {
				t.Errorf("preflight status = %d, want 204", resp.StatusCode)
			}
			if got := resp.Header.Get("Access-Control-Allow-Origin"); got != corsTestOrigin {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, corsTestOrigin)
			}
			if got := resp.Header.Get("Access-Control-Allow-Methods"); got == "" {
				t.Error("Access-Control-Allow-Methods empty on preflight")
			}
		})
	}
}

// Acceptance 2 (invariant guard): moving CORS must NOT displace the
// bearer layer from before the engine's contextInit — a valid token
// must still surface its principal in the ContextBuilder.
func TestCORSOrder_ValidToken_PrincipalReachesContextBuilder(t *testing.T) {
	svc := newCORSOrderService(t, WithCORS(corsTestOrigin))

	svc.SetContextBuilder(func(c *fiber.Ctx) (smokeAppCtx, error) {
		return smokeAppCtx{UserID: svc.Auth.Subject(c)}, nil
	})
	fibermap.RegisterHandler(svc.Engine, "cors.me",
		func(c *fibermap.Context[smokeAppCtx]) error {
			return c.JSON(map[string]string{"user_id": c.Data.UserID})
		})
	routesYAML := `
groups:
  - prefix: /
    routes:
      - method: GET
        path: /me
        handler: cors.me
        name: cors.me
`
	if err := svc.Engine.LoadBytes([]byte(routesYAML)); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}

	app := corsOrderApp(t, svc)
	if err := svc.Engine.Mount(app); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	token := mintCORSAccess(t, svc, "subject-cors")
	req := httptest.NewRequest("GET", "/me", nil)
	req.Header.Set("Origin", corsTestOrigin)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (valid token)", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["user_id"] != "subject-cors" {
		t.Errorf("ContextBuilder saw user_id = %q, want %q — app-bearer must stay before contextInit", body["user_id"], "subject-cors")
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != corsTestOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q on 200 too", got, corsTestOrigin)
	}
}
