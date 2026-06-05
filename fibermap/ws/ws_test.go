package ws_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"

	"github.com/theizzatbek/gokit/fibermap"
	fmws "github.com/theizzatbek/gokit/fibermap/ws"
)

type appCtx struct{}

// mount wires the engine with one WebSocket handler under `name` and
// mounts it at the supplied YAML route. Returns the running
// fiber.App so callers can app.Test() against it.
func mount(t *testing.T, name, path string, fn fmws.HandlerFunc[appCtx]) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{
		// Pass through fiber.ErrUpgradeRequired (HTTP 426).
		ErrorHandler: fiber.DefaultErrorHandler,
	})
	eng := fibermap.New[appCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (appCtx, error) { return appCtx{}, nil })
	fmws.Register(eng, name, fn)
	yaml := `
groups:
  - routes:
      - method: GET
        path: ` + path + `
        handler: ` + name + `
`
	if err := eng.LoadBytes([]byte(yaml)); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if err := eng.Mount(app); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return app
}

func TestWS_NonUpgradeRequest_Returns426(t *testing.T) {
	app := mount(t, "chat.connect", "/ws/chat",
		func(_ context.Context, _ *fibermap.Context[appCtx], _ *websocket.Conn) error {
			t.Error("handler should not run on plain GET")
			return nil
		})
	req := httptest.NewRequest("GET", "/ws/chat", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	// fiber.ErrUpgradeRequired maps to HTTP 426.
	if resp.StatusCode != 426 {
		t.Errorf("status = %d, want 426", resp.StatusCode)
	}
}

func TestWS_NilEngine_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil engine")
		}
	}()
	fmws.Register[appCtx](nil, "x", nil)
}

func TestWS_NilHandler_Panics(t *testing.T) {
	eng := fibermap.New[appCtx]()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil handler")
		}
	}()
	fmws.Register[appCtx](eng, "x", nil)
}

func TestWS_MultipleConfigs_Panics(t *testing.T) {
	eng := fibermap.New[appCtx]()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on multiple configs")
		}
	}()
	fmws.Register[appCtx](eng, "x",
		func(context.Context, *fibermap.Context[appCtx], *websocket.Conn) error { return nil },
		websocket.Config{}, websocket.Config{})
}
