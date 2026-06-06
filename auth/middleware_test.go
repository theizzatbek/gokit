package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth/internal/principalkey"
	"github.com/theizzatbek/gokit/errs"
)

// testErrHandler maps *errs.Error to its HTTP status. Local to middleware tests
// because importing fibermap would couple the auth package too tightly.
func testErrHandler(c *fiber.Ctx, err error) error {
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

func bearerApp(t *testing.T, mode BearerMode) (*fiber.App, *Auth[testClaims], string) {
	t.Helper()
	a := mustNewAuth(t)
	tok, err := a.Sign(Claims[testClaims]{
		Subject:   "u-1",
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
		Scopes:    []string{"a"},
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	app := fiber.New(fiber.Config{ErrorHandler: testErrHandler})
	app.Use(a.Bearer(mode))
	app.Get("/", func(c *fiber.Ctx) error {
		if _, ok := From[testClaims](c); ok {
			return c.SendStatus(http.StatusOK)
		}
		return c.SendStatus(http.StatusNoContent)
	})
	return app, a, tok
}

func TestBearer_Required_PassesValidToken(t *testing.T) {
	app, _, tok := bearerApp(t, BearerRequired)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestBearer_Required_401WhenMissing(t *testing.T) {
	app, _, _ := bearerApp(t, BearerRequired)
	resp, _ := app.Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got == "" {
		t.Fatalf("missing WWW-Authenticate header")
	}
}

func TestBearer_Required_401WhenWrongScheme(t *testing.T) {
	app, _, _ := bearerApp(t, BearerRequired)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestBearer_Required_401WhenForgedSignature(t *testing.T) {
	app, _, _ := bearerApp(t, BearerRequired)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJFZERTQSIsInR5cCI6IkpXVCIsImtpZCI6ImsxIn0.eyJzdWIiOiJ4In0.AAAAAA")
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestBearer_Optional_NoToken_PassesAnonymous(t *testing.T) {
	app, _, _ := bearerApp(t, BearerOptional)
	resp, _ := app.Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

func TestBearer_Optional_BadToken_StillRejects(t *testing.T) {
	app, _, _ := bearerApp(t, BearerOptional)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("optional must still reject present-but-invalid; got %d", resp.StatusCode)
	}
}

func TestRequireScope_AllPresentPasses(t *testing.T) {
	a := mustNewAuth(t)
	app := fiber.New(fiber.Config{ErrorHandler: testErrHandler})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals(principalkey.Key{}, &Principal[testClaims]{Scopes: []string{"a", "b", "c"}})
		return c.Next()
	})
	app.Use(a.RequireScope("a", "b"))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
	resp, _ := app.Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRequireScope_MissingScopeReturns403(t *testing.T) {
	a := mustNewAuth(t)
	app := fiber.New(fiber.Config{ErrorHandler: testErrHandler})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals(principalkey.Key{}, &Principal[testClaims]{Scopes: []string{"a"}})
		return c.Next()
	})
	app.Use(a.RequireScope("a", "missing"))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
	resp, _ := app.Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestRequireScope_NoPrincipalReturns500(t *testing.T) {
	a := mustNewAuth(t)
	app := fiber.New(fiber.Config{ErrorHandler: testErrHandler})
	app.Use(a.RequireScope("a"))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
	resp, _ := app.Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (programmer error)", resp.StatusCode)
	}
}

func TestRequireRole_AllPresentPasses(t *testing.T) {
	a := mustNewAuth(t)
	app := fiber.New(fiber.Config{ErrorHandler: testErrHandler})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals(principalkey.Key{}, &Principal[testClaims]{Roles: []string{"admin"}})
		return c.Next()
	})
	app.Use(a.RequireRole("admin"))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
	resp, _ := app.Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestRequireRole_MissingRoleReturns403(t *testing.T) {
	a := mustNewAuth(t)
	app := fiber.New(fiber.Config{ErrorHandler: testErrHandler})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals(principalkey.Key{}, &Principal[testClaims]{Roles: []string{"user"}})
		return c.Next()
	})
	app.Use(a.RequireRole("admin"))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
	resp, _ := app.Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestBearerFactory_ParsesRequired(t *testing.T) {
	a := mustNewAuth(t)
	h, err := a.BearerFactory([]any{"required"})
	if err != nil || h == nil {
		t.Fatalf("BearerFactory(required) err=%v h=%v", err, h)
	}
}

func TestBearerFactory_ParsesOptional(t *testing.T) {
	a := mustNewAuth(t)
	h, err := a.BearerFactory([]any{"optional"})
	if err != nil || h == nil {
		t.Fatalf("BearerFactory(optional) err=%v h=%v", err, h)
	}
}

func TestBearerFactory_DefaultsToRequiredWhenEmpty(t *testing.T) {
	a := mustNewAuth(t)
	if _, err := a.BearerFactory(nil); err != nil {
		t.Fatalf("BearerFactory(nil) err=%v", err)
	}
}

func TestBearerFactory_RejectsUnknownMode(t *testing.T) {
	a := mustNewAuth(t)
	_, err := a.BearerFactory([]any{"sometimes"})
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != CodeInvalidFactoryArgs {
		t.Fatalf("err=%v, want CodeInvalidFactoryArgs", err)
	}
}

func TestRequireScopeFactory_RejectsEmpty(t *testing.T) {
	a := mustNewAuth(t)
	_, err := a.RequireScopeFactory(nil)
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != CodeInvalidFactoryArgs {
		t.Fatalf("err=%v, want CodeInvalidFactoryArgs", err)
	}
}

func TestRequireScopeFactory_AcceptsStrings(t *testing.T) {
	a := mustNewAuth(t)
	if _, err := a.RequireScopeFactory([]any{"posts:read", "posts:write"}); err != nil {
		t.Fatalf("err=%v", err)
	}
}
