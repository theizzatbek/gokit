package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
)

func mustNewAuthWithStore(t *testing.T) *Auth[testClaims] {
	t.Helper()
	return mustNewAuth(t)
}

func TestRequireAnyScope_PassesOnOneMatch(t *testing.T) {
	a := mustNewAuthWithStore(t)
	tok, _ := a.Sign(Claims[testClaims]{
		Subject:   "u",
		Scopes:    []string{"orders:read"},
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
		IssuedAt:  time.Now().Unix(),
	})
	app := fiber.New(fiber.Config{ErrorHandler: errHandler})
	app.Get("/x", a.Bearer(BearerRequired), a.RequireAnyScope("orders:read", "admin:all"),
		func(c *fiber.Ctx) error { return c.SendStatus(204) })
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, _ := app.Test(req, -1)
	if resp.StatusCode != 204 {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
}

func TestRequireAnyScope_RejectsOnZeroMatches(t *testing.T) {
	a := mustNewAuthWithStore(t)
	tok, _ := a.Sign(Claims[testClaims]{
		Subject:   "u",
		Scopes:    []string{"profile:read"},
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
		IssuedAt:  time.Now().Unix(),
	})
	app := fiber.New(fiber.Config{ErrorHandler: errHandler})
	app.Get("/x", a.Bearer(BearerRequired), a.RequireAnyScope("orders:read", "admin:all"),
		func(c *fiber.Ctx) error { return c.SendStatus(204) })
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, _ := app.Test(req, -1)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestRequireAnyRole_PassesOnOneMatch(t *testing.T) {
	a := mustNewAuthWithStore(t)
	tok, _ := a.Sign(Claims[testClaims]{
		Subject:   "u",
		Roles:     []string{"editor"},
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
		IssuedAt:  time.Now().Unix(),
	})
	app := fiber.New(fiber.Config{ErrorHandler: errHandler})
	app.Get("/x", a.Bearer(BearerRequired), a.RequireAnyRole("admin", "editor"),
		func(c *fiber.Ctx) error { return c.SendStatus(204) })
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, _ := app.Test(req, -1)
	if resp.StatusCode != 204 {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
}

func errHandler(c *fiber.Ctx, err error) error {
	status := http.StatusInternalServerError
	if x, ok := err.(*xerrs.Error); ok {
		status, _ = xerrs.HTTP(x)
		return c.Status(status).SendString(x.Code)
	}
	return c.Status(status).SendString(err.Error())
}
