package fibermap

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func testHeadersFor(t *testing.T, opts ...SecurityHeadersOption) map[string]string {
	t.Helper()
	app := fiber.New()
	app.Use(SecurityHeaders(opts...))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("ok") })
	resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	headers := map[string]string{}
	for k := range resp.Header {
		headers[k] = resp.Header.Get(k)
	}
	return headers
}

func TestSecurityHeaders_DefaultsAllInstalled(t *testing.T) {
	h := testHeadersFor(t)
	checks := map[string]string{
		"Strict-Transport-Security": "max-age=31536000",
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Content-Security-Policy":   "default-src 'self'",
	}
	for k, want := range checks {
		got := h[k]
		if got == "" {
			t.Errorf("%s missing; got headers: %v", k, h)
			continue
		}
		if !strings.Contains(got, want) {
			t.Errorf("%s = %q, want contains %q", k, got, want)
		}
	}
}

func TestSecurityHeaders_HSTSIncludeSubdomainsPreload(t *testing.T) {
	h := testHeadersFor(t, WithHSTSIncludeSubdomains(), WithHSTSPreload())
	v := h["Strict-Transport-Security"]
	for _, want := range []string{"max-age=31536000", "includeSubDomains", "preload"} {
		if !strings.Contains(v, want) {
			t.Errorf("HSTS = %q, want contains %q", v, want)
		}
	}
}

func TestSecurityHeaders_CustomHSTSMaxAge(t *testing.T) {
	h := testHeadersFor(t, WithHSTSMaxAge(3600))
	if v := h["Strict-Transport-Security"]; !strings.Contains(v, "max-age=3600") {
		t.Errorf("HSTS = %q, want max-age=3600", v)
	}
}

func TestSecurityHeaders_WithoutHSTS(t *testing.T) {
	h := testHeadersFor(t, WithoutHSTS())
	if v := h["Strict-Transport-Security"]; v != "" {
		t.Errorf("HSTS should be empty when WithoutHSTS; got %q", v)
	}
	if h["X-Content-Type-Options"] == "" {
		t.Error("X-Content-Type-Options should still install when WithoutHSTS")
	}
}

func TestSecurityHeaders_WithoutCSP(t *testing.T) {
	h := testHeadersFor(t, WithoutCSP())
	if v := h["Content-Security-Policy"]; v != "" {
		t.Errorf("CSP should be empty when WithoutCSP; got %q", v)
	}
}

func TestSecurityHeaders_CustomCSP(t *testing.T) {
	custom := "default-src 'none'; script-src 'self'"
	h := testHeadersFor(t, WithCSP(custom))
	if v := h["Content-Security-Policy"]; v != custom {
		t.Errorf("CSP = %q, want %q", v, custom)
	}
}

func TestSecurityHeaders_CustomFrameOptionsAndReferrer(t *testing.T) {
	h := testHeadersFor(t,
		WithFrameOptions("SAMEORIGIN"),
		WithReferrerPolicy("no-referrer"))
	if v := h["X-Frame-Options"]; v != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q, want SAMEORIGIN", v)
	}
	if v := h["Referrer-Policy"]; v != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", v)
	}
}

func TestSecurityHeaders_HeadersSurviveErrorResponses(t *testing.T) {
	// Errors from downstream handlers still trigger headers because
	// the middleware writes via c.Set BEFORE c.Next() — Fiber's
	// response phase reads the headers off the ctx after Next().
	app := fiber.New()
	app.Use(SecurityHeaders())
	app.Get("/", func(c *fiber.Ctx) error {
		return fiber.NewError(fiber.StatusBadRequest, "nope")
	})
	resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if v := resp.Header.Get("X-Content-Type-Options"); v != "nosniff" {
		t.Errorf("nosniff should survive 4xx; got %q", v)
	}
	if v := resp.Header.Get("Strict-Transport-Security"); v == "" {
		t.Error("HSTS should survive 4xx")
	}
}
