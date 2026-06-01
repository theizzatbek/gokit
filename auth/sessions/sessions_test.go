package sessions_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/auth/sessions"
	"github.com/theizzatbek/gokit/fibermap"
)

type myClaims struct {
	Plan string `json:"plan"`
}

func makeAuth(t *testing.T) *auth.Auth[myClaims] {
	t.Helper()
	keys, err := auth.GenerateEd25519Key("k1")
	if err != nil {
		t.Fatalf("GenerateEd25519Key: %v", err)
	}
	a, err := auth.New[myClaims](auth.Config{
		Issuer:     "test",
		Audience:   []string{"web"},
		Keys:       keys,
		AccessTTL:  time.Minute,
		RefreshTTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	return a
}

func TestManager_IssueAndMiddlewareRoundTrip(t *testing.T) {
	a := makeAuth(t)
	store := sessions.NewMemoryStore()
	sm, err := a.Sessions(sessions.Config{
		Store:          store,
		TTL:            time.Hour,
		IdleTimeout:    15 * time.Minute,
		InsecureCookie: true,
	})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}

	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
	app.Post("/login", func(c *fiber.Ctx) error {
		return sm.Issue(c, "u-42", myClaims{Plan: "pro"}, []string{"read"}, nil)
	})
	app.Get("/me", sm.Middleware(sessions.Required), func(c *fiber.Ctx) error {
		p, ok := auth.From[myClaims](c)
		if !ok || p.Subject != "u-42" {
			return c.Status(500).SendString("no principal")
		}
		return c.JSON(p)
	})

	// Login → set cookie.
	resp, err := app.Test(httptest.NewRequest("POST", "/login", nil))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	cookie := resp.Header.Get("Set-Cookie")
	if !strings.Contains(cookie, "sid=") {
		t.Fatalf("missing sid cookie: %q", cookie)
	}

	// Reuse cookie → protected route works + principal populated.
	req := httptest.NewRequest("GET", "/me", nil)
	req.Header.Set("Cookie", cookie)
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("me status = %d, want 200", resp.StatusCode)
	}
}

func TestManager_RequiredModeMissingCookieReturns401(t *testing.T) {
	a := makeAuth(t)
	sm, _ := a.Sessions(sessions.Config{
		Store: sessions.NewMemoryStore(), TTL: time.Hour, InsecureCookie: true,
	})
	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
	app.Get("/me", sm.Middleware(sessions.Required), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	resp, _ := app.Test(httptest.NewRequest("GET", "/me", nil))
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestManager_OptionalModePassesThrough(t *testing.T) {
	a := makeAuth(t)
	sm, _ := a.Sessions(sessions.Config{
		Store: sessions.NewMemoryStore(), TTL: time.Hour, InsecureCookie: true,
	})
	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
	app.Get("/me", sm.Middleware(sessions.Optional), func(c *fiber.Ctx) error {
		_, has := auth.From[myClaims](c)
		if has {
			return c.SendString("auth")
		}
		return c.SendString("anon")
	})

	resp, _ := app.Test(httptest.NewRequest("GET", "/me", nil))
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestManager_LogoutDeletesSession(t *testing.T) {
	a := makeAuth(t)
	store := sessions.NewMemoryStore()
	sm, _ := a.Sessions(sessions.Config{
		Store: store, TTL: time.Hour, InsecureCookie: true,
	})
	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
	app.Post("/login", func(c *fiber.Ctx) error {
		return sm.Issue(c, "u-42", myClaims{}, nil, nil)
	})
	app.Post("/logout", func(c *fiber.Ctx) error {
		return sm.Logout(c)
	})

	resp, _ := app.Test(httptest.NewRequest("POST", "/login", nil))
	cookie := resp.Header.Get("Set-Cookie")

	if got := len(store.Snapshot()); got != 1 {
		t.Fatalf("Snapshot len = %d, want 1 after login", got)
	}
	req := httptest.NewRequest("POST", "/logout", nil)
	req.Header.Set("Cookie", cookie)
	_, _ = app.Test(req)
	if got := len(store.Snapshot()); got != 0 {
		t.Errorf("Snapshot len after logout = %d, want 0", got)
	}
}

func TestManager_LogoutEverywhereKillsAllForSubject(t *testing.T) {
	a := makeAuth(t)
	store := sessions.NewMemoryStore()
	sm, _ := a.Sessions(sessions.Config{
		Store: store, TTL: time.Hour, InsecureCookie: true,
	})
	app := fiber.New()
	app.Post("/login", func(c *fiber.Ctx) error {
		return sm.Issue(c, "u-42", myClaims{}, nil, nil)
	})
	for i := 0; i < 3; i++ {
		_, _ = app.Test(httptest.NewRequest("POST", "/login", nil))
	}
	if got := len(store.Snapshot()); got != 3 {
		t.Fatalf("Snapshot = %d, want 3", got)
	}
	_ = sm.LogoutEverywhere(context.Background(), "u-42")
	if got := len(store.Snapshot()); got != 0 {
		t.Errorf("Snapshot after LogoutEverywhere = %d, want 0", got)
	}
}

func TestManager_ExpiredSessionIsRejected(t *testing.T) {
	a := makeAuth(t)
	store := sessions.NewMemoryStore()
	sm, _ := a.Sessions(sessions.Config{
		Store: store, TTL: time.Millisecond, InsecureCookie: true,
	})
	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
	app.Post("/login", func(c *fiber.Ctx) error {
		return sm.Issue(c, "u-42", myClaims{}, nil, nil)
	})
	app.Get("/me", sm.Middleware(sessions.Required), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	resp, _ := app.Test(httptest.NewRequest("POST", "/login", nil))
	cookie := resp.Header.Get("Set-Cookie")
	time.Sleep(20 * time.Millisecond)

	req := httptest.NewRequest("GET", "/me", nil)
	req.Header.Set("Cookie", cookie)
	resp, _ = app.Test(req)
	if resp.StatusCode != 401 {
		t.Errorf("status after expiry = %d, want 401", resp.StatusCode)
	}
}

func TestManager_RequiresValidConfig(t *testing.T) {
	a := makeAuth(t)
	if _, err := a.Sessions(sessions.Config{TTL: time.Hour}); err == nil {
		t.Error("expected error for missing Store")
	}
	if _, err := a.Sessions(sessions.Config{Store: sessions.NewMemoryStore()}); err == nil {
		t.Error("expected error for TTL <= 0")
	}
}

func TestManager_TamperedCookieReturns401(t *testing.T) {
	a := makeAuth(t)
	sm, _ := a.Sessions(sessions.Config{
		Store: sessions.NewMemoryStore(), TTL: time.Hour, InsecureCookie: true,
	})
	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
	app.Get("/me", sm.Middleware(sessions.Required), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})

	req := httptest.NewRequest("GET", "/me", nil)
	req.Header.Set("Cookie", "sid=not-a-valid-id-bro")
	resp, _ := app.Test(req)
	if resp.StatusCode != 401 {
		t.Errorf("status with tampered cookie = %d, want 401", resp.StatusCode)
	}
}
