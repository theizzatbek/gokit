package fibermount_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/auth/fibermount"
	"github.com/theizzatbek/gokit/fibermap"
)

type appClaims struct{}
type appCtx struct{}

// TestMountMiddlewareFactories_BearerWorksThroughEngine wires auth's bearer
// factory into a real *fibermap.Engine[T] via fibermount and exercises a
// request end-to-end: registration → LoadBytes → Mount → HTTP request.
// A missing Authorization header must surface as 401 from the bearer factory.
func TestMountMiddlewareFactories_BearerWorksThroughEngine(t *testing.T) {
	keys, err := auth.GenerateEd25519Key("k1")
	if err != nil {
		t.Fatalf("GenerateEd25519Key: %v", err)
	}
	a, err := auth.New[appClaims](auth.Config{
		Issuer:     "test",
		Audience:   []string{"web"},
		Keys:       keys,
		AccessTTL:  time.Minute,
		RefreshTTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}

	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
	eng := fibermap.New[appCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (appCtx, error) { return appCtx{}, nil })
	fibermap.RegisterHandler(eng, "ping", func(c *fibermap.Context[appCtx]) error {
		return c.Ctx.SendStatus(http.StatusOK)
	})

	if err := fibermount.MountMiddlewareFactories(eng, a); err != nil {
		t.Fatalf("MountMiddlewareFactories: %v", err)
	}

	const yamlConfig = `
groups:
  - prefix: /api
    middleware:
      - { bearer: ["required"] }
    routes:
      - { method: GET, path: /ping, handler: ping }
`
	if err := eng.LoadBytes([]byte(yamlConfig)); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if err := eng.Mount(app); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	// Request without bearer → 401 from bearer middleware.
	resp, err := app.Test(httptest.NewRequest("GET", "/api/ping", nil))
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got == "" {
		t.Fatalf("missing WWW-Authenticate header — bearer middleware did not run")
	}
}

// TestMountMiddlewareFactories_BearerAcceptsValidToken complements the 401
// negative case: a request with a valid Bearer token should reach the handler
// and return 200, proving the full factory→fibermap chain is wired correctly.
func TestMountMiddlewareFactories_BearerAcceptsValidToken(t *testing.T) {
	keys, err := auth.GenerateEd25519Key("k1")
	if err != nil {
		t.Fatalf("GenerateEd25519Key: %v", err)
	}
	a, err := auth.New[appClaims](auth.Config{
		Issuer:     "test",
		Audience:   []string{"web"},
		Keys:       keys,
		AccessTTL:  time.Minute,
		RefreshTTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}

	tok, err := a.Sign(auth.Claims[appClaims]{
		Subject:   "u-1",
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
	eng := fibermap.New[appCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (appCtx, error) { return appCtx{}, nil })
	fibermap.RegisterHandler(eng, "ping", func(c *fibermap.Context[appCtx]) error {
		return c.Ctx.SendStatus(http.StatusOK)
	})
	if err := fibermount.MountMiddlewareFactories(eng, a); err != nil {
		t.Fatalf("MountMiddlewareFactories: %v", err)
	}

	const yamlConfig = `
groups:
  - prefix: /api
    middleware:
      - { bearer: ["required"] }
    routes:
      - { method: GET, path: /ping, handler: ping }
`
	if err := eng.LoadBytes([]byte(yamlConfig)); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if err := eng.Mount(app); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/ping", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
