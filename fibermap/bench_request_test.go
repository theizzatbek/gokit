package fibermap_test

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/gokit/fibermap"
)

// BenchmarkEngine_ServeRequest measures a full request through a mounted
// engine: the contextInit middleware (builder + Locals), the per-route
// wrapper, and the handler. Complements BenchmarkEngine_Lookup, which
// isolates route resolution.
func BenchmarkEngine_ServeRequest(b *testing.B) {
	eng := fibermap.New[docCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (docCtx, error) {
		return docCtx{UserID: "u-1"}, nil
	})
	eng.RegisterHandler("ping", func(c *docCtxRef) error {
		return c.SendString("pong " + c.Data.UserID)
	})
	if err := eng.LoadBytes([]byte(
		"groups:\n  - prefix: /v1\n    routes:\n      - {method: GET, path: /ping, handler: ping}",
	)); err != nil {
		b.Fatal(err)
	}
	app := fiber.New()
	if err := eng.Mount(app); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := app.Test(httptest.NewRequest("GET", "/v1/ping", nil))
		if err != nil {
			b.Fatal(err)
		}
		_ = resp.Body.Close()
	}
}
