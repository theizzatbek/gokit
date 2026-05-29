package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

// runCORS mounts the supplied options' fiberMiddleware on a fresh app
// and returns it. Skips going through full service.New so the test
// stays focused on the CORS wiring.
func runCORS(opt Option) *fiber.App {
	o := &options{}
	opt(o)
	app := fiber.New()
	for _, mw := range o.fiberMiddleware {
		app.Use(mw)
	}
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendString("ok") })
	return app
}

func TestWithCORS_ExplicitOrigin_AllowsCredentials(t *testing.T) {
	app := runCORS(WithCORS("https://app.example.com"))

	// Preflight (OPTIONS) for an explicit origin.
	req := httptest.NewRequest("OPTIONS", "/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Authorization")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("Allow-Origin = %q, want app origin", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want true (explicit origin → credentials on)", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); got == "" {
		t.Errorf("Allow-Methods empty: %v", resp.Header)
	}
}

func TestWithCORS_Wildcard_DisablesCredentials(t *testing.T) {
	app := runCORS(WithCORS("*"))

	req := httptest.NewRequest("OPTIONS", "/x", nil)
	req.Header.Set("Origin", "https://random.example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")

	resp, _ := app.Test(req)
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got == "" {
		t.Errorf("Allow-Origin empty for *")
	}
	if got := resp.Header.Get("Access-Control-Allow-Credentials"); got == "true" {
		t.Errorf("Allow-Credentials = true with *, CORS spec forbids this")
	}
}

func TestWithCORS_MultipleOrigins(t *testing.T) {
	app := runCORS(WithCORS("https://a.com", "https://b.com"))

	for _, origin := range []string{"https://a.com", "https://b.com"} {
		req := httptest.NewRequest("OPTIONS", "/x", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "GET")
		resp, _ := app.Test(req)
		resp.Body.Close()
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("origin %q: Allow-Origin = %q, want %q", origin, got, origin)
		}
	}
}

func TestWithCORSConfig_PassesThrough(t *testing.T) {
	custom := cors.Config{
		AllowOrigins:     "https://only.example.com",
		AllowMethods:     "POST",
		AllowCredentials: true,
	}
	app := runCORS(WithCORSConfig(custom))

	req := httptest.NewRequest("OPTIONS", "/x", nil)
	req.Header.Set("Origin", "https://only.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, _ := app.Test(req)
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://only.example.com" {
		t.Errorf("Allow-Origin = %q", got)
	}
}

func TestContainsWildcardOrigin(t *testing.T) {
	cases := []struct {
		in   []string
		want bool
	}{
		{[]string{"*"}, true},
		{[]string{"https://a.com", "*"}, true},
		{[]string{" * "}, true}, // whitespace-tolerant
		{[]string{"https://a.com"}, false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := containsWildcardOrigin(tc.in); got != tc.want {
			t.Errorf("containsWildcardOrigin(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
