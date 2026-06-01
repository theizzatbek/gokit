package dev_test

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap/dev"
)

func TestErrorHandler_HTMLOnHTMLAccept(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: dev.ErrorHandler(nil)})
	app.Get("/boom", func(c *fiber.Ctx) error {
		return xerrs.NotFoundf("widget_not_found", "no widget %s", "abc")
	})
	req := httptest.NewRequest("GET", "/boom", nil)
	req.Header.Set("Accept", "text/html")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<html") {
		t.Errorf("not HTML: %s", body[:200])
	}
	if !strings.Contains(string(body), "widget_not_found") {
		t.Errorf("body missing error code: %s", body[:500])
	}
	if !strings.Contains(string(body), "no widget abc") {
		t.Errorf("body missing message: %s", body[:500])
	}
}

func TestErrorHandler_JSONFallbackForNonHTML(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: dev.ErrorHandler(nil)})
	app.Get("/boom", func(c *fiber.Ctx) error {
		return xerrs.NotFound("missing", "gone")
	})
	req := httptest.NewRequest("GET", "/boom", nil)
	req.Header.Set("Accept", "application/json")
	resp, _ := app.Test(req)
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "<html") {
		t.Errorf("JSON client got HTML: %s", body[:200])
	}
	if !strings.Contains(string(body), "missing") {
		t.Errorf("JSON body missing code: %s", body)
	}
}

func TestRoutesHandler_ListsMountedRoutes(t *testing.T) {
	app := fiber.New()
	app.Get("/a", func(c *fiber.Ctx) error { return nil })
	app.Post("/b", func(c *fiber.Ctx) error { return nil })
	app.Get("/_dev/routes", dev.RoutesHandler(app))

	// JSON response.
	req := httptest.NewRequest("GET", "/_dev/routes", nil)
	req.Header.Set("Accept", "application/json")
	resp, _ := app.Test(req)
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"/a"`) {
		t.Errorf("JSON missing /a: %s", body)
	}
	if !strings.Contains(string(body), `"/b"`) {
		t.Errorf("JSON missing /b: %s", body)
	}

	// HTML response.
	req = httptest.NewRequest("GET", "/_dev/routes", nil)
	req.Header.Set("Accept", "text/html")
	resp, _ = app.Test(req)
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<table") {
		t.Errorf("HTML missing table: %s", body[:300])
	}
}

func TestConfigHandler_RedactsSecretEnvVars(t *testing.T) {
	t.Setenv("MY_DATABASE_PASSWORD", "super-secret")
	t.Setenv("MY_PUBLIC_VAR", "harmless")

	app := fiber.New()
	app.Get("/_dev/config", dev.ConfigHandler())

	req := httptest.NewRequest("GET", "/_dev/config", nil)
	req.Header.Set("Accept", "application/json")
	resp, _ := app.Test(req)
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if strings.Contains(s, "super-secret") {
		t.Errorf("secret leaked: %s", s)
	}
	if !strings.Contains(s, `"MY_DATABASE_PASSWORD"`) {
		t.Errorf("env var name not listed: %s", s)
	}
	if !strings.Contains(s, `"harmless"`) {
		t.Errorf("public var should not be redacted: %s", s)
	}
}

func TestConfigHandler_ExtraRedaction(t *testing.T) {
	t.Setenv("APP_CUSTOM_INTERNAL", "private")
	app := fiber.New()
	app.Get("/_dev/config", dev.ConfigHandler(dev.WithExtraRedaction("INTERNAL")))
	req := httptest.NewRequest("GET", "/_dev/config", nil)
	req.Header.Set("Accept", "application/json")
	resp, _ := app.Test(req)
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "private") {
		t.Errorf("extra-redaction substring not honoured: %s", body)
	}
}
