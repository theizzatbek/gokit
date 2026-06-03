package auth

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestWithIPExtractor_OverridesClientIP(t *testing.T) {
	ks, _ := GenerateEd25519Key("k1")
	const wantIP = "203.0.113.42"
	a, err := New[testClaims](Config{
		Keys: ks, AccessTTL: time.Minute, RefreshTTL: time.Hour,
	},
		WithIPExtractor(func(c *fiber.Ctx) string {
			return c.Get("CF-Connecting-IP")
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	var got string
	app.Get("/who", func(c *fiber.Ctx) error {
		got = a.clientIP(c)
		return c.SendStatus(204)
	})
	req := httptest.NewRequest("GET", "/who", nil)
	req.Header.Set("CF-Connecting-IP", wantIP)
	_, _ = app.Test(req, -1)
	if got != wantIP {
		t.Errorf("clientIP = %q, want %q", got, wantIP)
	}
}

func TestWithIPExtractor_EmptyFallsBackToFiber(t *testing.T) {
	ks, _ := GenerateEd25519Key("k1")
	a, _ := New[testClaims](Config{
		Keys: ks, AccessTTL: time.Minute, RefreshTTL: time.Hour,
	}, WithIPExtractor(func(c *fiber.Ctx) string { return "" }))

	app := fiber.New()
	var got string
	app.Get("/who", func(c *fiber.Ctx) error {
		got = a.clientIP(c)
		return c.SendStatus(204)
	})
	_, _ = app.Test(httptest.NewRequest("GET", "/who", nil), -1)
	// fiber returns "0.0.0.0" for an unaddressed test req, but
	// importantly NOT "".
	if got == "" {
		t.Error("clientIP returned empty — should fall back to c.IP()")
	}
}
