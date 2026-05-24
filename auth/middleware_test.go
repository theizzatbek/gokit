package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/fibermap/errs"
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
