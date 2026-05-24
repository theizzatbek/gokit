package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/fibermap/auth"
	"github.com/theizzatbek/fibermap/auth/internal/memstore"
	"github.com/theizzatbek/fibermap/errs"
)

// appClaims is the custom-claims type used by handler tests.
type appClaims struct {
	TenantID string `json:"tenant_id,omitempty"`
}

// testErrorHandler maps *errs.Error to its HTTP status. Local to handlers tests
// to avoid taking a runtime dependency on fibermap.
func testErrorHandler(c *fiber.Ctx, err error) error {
	var e *errs.Error
	if errors.As(err, &e) {
		status, body := errs.HTTP(err)
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return c.Status(status).JSON(body)
	}
	return fiber.DefaultErrorHandler(c, err)
}

// newAuth builds an *auth.Auth[appClaims] with a fresh in-memory refresh store.
// Equivalent to mustNewAuth used in internal tests, but built for external use.
func newAuth(t *testing.T) *auth.Auth[appClaims] {
	t.Helper()
	keys, err := auth.GenerateEd25519Key("k1")
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	a, err := auth.New[appClaims](auth.Config{
		Issuer: "myapp", Audience: []string{"web"},
		Keys: keys, AccessTTL: 15 * time.Minute, RefreshTTL: 30 * 24 * time.Hour,
	}, auth.WithRefreshStore(memstore.New()), auth.WithCookieSecure(false))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func loginApp(t *testing.T, verifier auth.CredentialsVerifier[appClaims]) *fiber.App {
	t.Helper()
	a := newAuth(t)
	a.SetCredentialsVerifier(verifier)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Post("/auth/login", a.LoginHandler)
	return app
}

func TestLogin_HappyPath(t *testing.T) {
	app := loginApp(t, func(ctx context.Context, r auth.LoginRequest) (auth.LoginResult[appClaims], error) {
		if r.Login != "alice" || r.Password != "hunter2" {
			return auth.LoginResult[appClaims]{}, errs.Unauthorized(auth.CodeInvalidCredentials, "no")
		}
		return auth.LoginResult[appClaims]{Subject: "u-1", Scopes: []string{"posts:read"}}, nil
	})
	req := httptest.NewRequest("POST", "/auth/login",
		strings.NewReader(`{"login":"alice","password":"hunter2"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
		Subject     string `json:"subject"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.AccessToken == "" || body.TokenType != "Bearer" {
		t.Fatalf("bad body: %+v", body)
	}
	if body.Subject != "u-1" {
		t.Fatalf("subject = %q", body.Subject)
	}
	// Refresh cookie must be set, HttpOnly, on /auth.
	var refreshCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "refresh_token" {
			refreshCookie = c
		}
	}
	if refreshCookie == nil {
		t.Fatalf("no refresh_token cookie")
	}
	if !refreshCookie.HttpOnly {
		t.Errorf("refresh cookie not HttpOnly")
	}
	if refreshCookie.Path != "/auth" {
		t.Errorf("refresh cookie path = %q, want /auth", refreshCookie.Path)
	}
	if !strings.HasPrefix(refreshCookie.Value, "rt_") {
		t.Errorf("refresh value not prefixed: %q", refreshCookie.Value)
	}
}

func TestLogin_BadCredentialsIs401(t *testing.T) {
	app := loginApp(t, func(ctx context.Context, r auth.LoginRequest) (auth.LoginResult[appClaims], error) {
		return auth.LoginResult[appClaims]{}, errs.Unauthorized(auth.CodeInvalidCredentials, "invalid login or password")
	})
	req := httptest.NewRequest("POST", "/auth/login",
		strings.NewReader(`{"login":"x","password":"y"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestLogin_MalformedBodyIs400(t *testing.T) {
	app := loginApp(t, func(ctx context.Context, r auth.LoginRequest) (auth.LoginResult[appClaims], error) {
		return auth.LoginResult[appClaims]{Subject: "u-1"}, nil
	})
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"login":""}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestLogin_NoVerifierIs500(t *testing.T) {
	a := newAuth(t)
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Post("/auth/login", a.LoginHandler)
	req := httptest.NewRequest("POST", "/auth/login",
		strings.NewReader(`{"login":"a","password":"b"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}

func refreshAppWithStore(t *testing.T, store auth.RefreshStore, refresher auth.ClaimsRefresher[appClaims]) (*fiber.App, *auth.Auth[appClaims]) {
	t.Helper()
	ks, _ := auth.GenerateEd25519Key("k1")
	a, _ := auth.New[appClaims](auth.Config{
		Issuer: "myapp", Audience: []string{"web"},
		Keys: ks, AccessTTL: 15 * time.Minute, RefreshTTL: 30 * 24 * time.Hour,
	}, auth.WithRefreshStore(store), auth.WithCookieSecure(false))
	a.SetCredentialsVerifier(func(ctx context.Context, r auth.LoginRequest) (auth.LoginResult[appClaims], error) {
		return auth.LoginResult[appClaims]{Subject: "u-1"}, nil
	})
	if refresher != nil {
		a.SetClaimsRefresher(refresher)
	}
	app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
	app.Post("/auth/login", a.LoginHandler)
	app.Post("/auth/refresh", a.RefreshHandler)
	return app, a
}

func loginAndGetRefreshCookie(t *testing.T, app *fiber.App) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest("POST", "/auth/login",
		strings.NewReader(`{"login":"a","password":"b"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "refresh_token" {
			return c
		}
	}
	t.Fatalf("login did not return refresh cookie")
	return nil
}

func TestRefresh_RotatesAndIssuesNewPair(t *testing.T) {
	store := memstore.New()
	app, _ := refreshAppWithStore(t, store, nil)
	rc := loginAndGetRefreshCookie(t, app)

	req := httptest.NewRequest("POST", "/auth/refresh", nil)
	req.AddCookie(rc)
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d", resp.StatusCode)
	}
	// New refresh cookie issued, value differs from the original.
	var newRC *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "refresh_token" {
			newRC = c
		}
	}
	if newRC == nil {
		t.Fatalf("refresh did not set new cookie")
	}
	if newRC.Value == rc.Value {
		t.Fatalf("refresh value did not rotate")
	}
}

func TestRefresh_ReuseDetected_RevokesFamily(t *testing.T) {
	store := memstore.New()
	app, _ := refreshAppWithStore(t, store, nil)
	rc := loginAndGetRefreshCookie(t, app)

	// First refresh: ok.
	req := httptest.NewRequest("POST", "/auth/refresh", nil)
	req.AddCookie(rc)
	resp1, _ := app.Test(req)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first refresh = %d", resp1.StatusCode)
	}
	// Second refresh with the OLD cookie: store sees already-consumed, must
	// 401 with refresh_reused and revoke the family — so the new cookie also
	// stops working on its next /refresh.
	req2 := httptest.NewRequest("POST", "/auth/refresh", nil)
	req2.AddCookie(rc)
	resp2, _ := app.Test(req2)
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reuse status = %d, want 401", resp2.StatusCode)
	}
	// And the NEW cookie issued in resp1 must now fail because its family was revoked.
	var newRC *http.Cookie
	for _, c := range resp1.Cookies() {
		if c.Name == "refresh_token" {
			newRC = c
		}
	}
	req3 := httptest.NewRequest("POST", "/auth/refresh", nil)
	req3.AddCookie(newRC)
	resp3, _ := app.Test(req3)
	if resp3.StatusCode != http.StatusUnauthorized {
		t.Fatalf("post-revoke refresh = %d, want 401", resp3.StatusCode)
	}
}

func TestRefresh_MissingCookieIs401(t *testing.T) {
	store := memstore.New()
	app, _ := refreshAppWithStore(t, store, nil)
	resp, _ := app.Test(httptest.NewRequest("POST", "/auth/refresh", nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestRefresh_ClaimsRefresherInvoked(t *testing.T) {
	store := memstore.New()
	var called bool
	app, _ := refreshAppWithStore(t, store, func(ctx context.Context, sub string) (auth.LoginResult[appClaims], error) {
		called = true
		return auth.LoginResult[appClaims]{Subject: sub, Scopes: []string{"refreshed"}}, nil
	})
	rc := loginAndGetRefreshCookie(t, app)
	req := httptest.NewRequest("POST", "/auth/refresh", nil)
	req.AddCookie(rc)
	app.Test(req)
	if !called {
		t.Fatalf("ClaimsRefresher was not invoked")
	}
}
