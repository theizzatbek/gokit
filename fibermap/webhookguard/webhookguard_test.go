package webhookguard

import (
	"bytes"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/clients/webhooks/verifiers"
	"github.com/theizzatbek/gokit/fibermap"
)

func TestMiddleware_AllowsValid(t *testing.T) {
	secret := []byte("abc")
	v := verifiers.NewGitHub(secret)

	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
	app.Use(New(v))
	app.Post("/hook", func(c *fiber.Ctx) error {
		if string(c.Body()) != `{"x":1}` {
			t.Fatalf("downstream body unexpected: %s", c.Body())
		}
		return c.SendString("ok")
	})

	body := []byte(`{"x":1}`)
	req := httptest.NewRequest("POST", "/hook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256="+mustMAC(secret, body))

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		got, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, got)
	}
}

func TestMiddleware_Rejects(t *testing.T) {
	v := verifiers.NewGitHub([]byte("right"))
	app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
	app.Use(New(v))
	app.Post("/hook", func(c *fiber.Ctx) error { return c.SendString("never") })

	req := httptest.NewRequest("POST", "/hook", bytes.NewReader([]byte("{}")))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	resp, _ := app.Test(req)
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}
