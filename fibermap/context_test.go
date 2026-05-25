package fibermap

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

type tCtx struct {
	UserID string
	Role   string
}

func TestContext_AccessFiberAndData(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		ctx := &Context[tCtx]{Ctx: c, Data: tCtx{UserID: "u1", Role: "doctor"}}
		if ctx.Data.UserID != "u1" {
			t.Errorf("Data.UserID = %q", ctx.Data.UserID)
		}
		// Fiber method via embedding:
		return ctx.Status(fiber.StatusTeapot).SendString("ok")
	})

	req := httptest.NewRequest("GET", "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusTeapot {
		t.Errorf("status = %d, want %d", resp.StatusCode, fiber.StatusTeapot)
	}
}
