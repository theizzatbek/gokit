package fibermap

import (
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestRequestID_GeneratesWhenMissing(t *testing.T) {
	app := fiber.New()
	app.Use(RequestID())
	app.Get("/x", func(c *fiber.Ctx) error {
		id, _ := c.Locals(LocalsRequestID).(string)
		return c.SendString(id)
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatal(err)
	}

	got := resp.Header.Get(HeaderRequestID)
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(got) {
		t.Errorf("X-Request-ID = %q, want 16 lowercase hex chars", got)
	}
}

func TestRequestID_PreservesIncoming(t *testing.T) {
	app := fiber.New()
	app.Use(RequestID())
	app.Get("/x", func(c *fiber.Ctx) error {
		id, _ := c.Locals(LocalsRequestID).(string)
		return c.SendString(id)
	})

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set(HeaderRequestID, "client-supplied-id-123")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}

	if got := resp.Header.Get(HeaderRequestID); got != "client-supplied-id-123" {
		t.Errorf("response X-Request-ID = %q, want client-supplied-id-123", got)
	}
}

func TestRequestID_PopulatesLocals(t *testing.T) {
	app := fiber.New()
	app.Use(RequestID())

	var captured string
	app.Get("/x", func(c *fiber.Ctx) error {
		captured, _ = c.Locals(LocalsRequestID).(string)
		return c.SendString("ok")
	})

	if _, err := app.Test(httptest.NewRequest("GET", "/x", nil)); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 16 {
		t.Errorf("Locals(request_id) = %q, want 16 chars", captured)
	}
}
