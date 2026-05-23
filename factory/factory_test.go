package factory_test

import (
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/fibermap"
	"github.com/theizzatbek/fibermap/factory"
)

type appCtx struct {
	Role   string
	Scopes []string
}

func buildEngine(t *testing.T, yaml string, register func(*fibermap.Engine[appCtx]), role string, scopes []string) *fiber.App {
	t.Helper()
	e := fibermap.New[appCtx]()
	e.SetContextBuilder(func(c *fiber.Ctx) (appCtx, error) {
		return appCtx{Role: role, Scopes: scopes}, nil
	})
	e.RegisterHandler("ok", func(c *fibermap.Context[appCtx]) error {
		return c.SendString("ok")
	})
	register(e)
	if err := e.LoadBytes([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestRequireRole_Allowed(t *testing.T) {
	app := buildEngine(t, `
groups:
  - routes:
      - method: GET
        path: /x
        handler: ok
        middleware: [{require_role: [director]}]
`, func(e *fibermap.Engine[appCtx]) {
		e.RegisterMiddlewareFactory("require_role",
			factory.RequireRole(func(c *fibermap.Context[appCtx]) string { return c.Data.Role }),
		)
	}, "director", nil)

	resp, _ := app.Test(httptest.NewRequest("GET", "/x", nil))
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRequireRole_Denied(t *testing.T) {
	app := buildEngine(t, `
groups:
  - routes:
      - method: GET
        path: /x
        handler: ok
        middleware: [{require_role: [director]}]
`, func(e *fibermap.Engine[appCtx]) {
		e.RegisterMiddlewareFactory("require_role",
			factory.RequireRole(func(c *fibermap.Context[appCtx]) string { return c.Data.Role }),
		)
	}, "guest", nil)

	resp, _ := app.Test(httptest.NewRequest("GET", "/x", nil))
	if resp.StatusCode != fiber.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestRequireRole_NoArgs_FailsMount(t *testing.T) {
	e := fibermap.New[appCtx]()
	e.SetContextBuilder(func(c *fiber.Ctx) (appCtx, error) { return appCtx{}, nil })
	e.RegisterHandler("ok", func(c *fibermap.Context[appCtx]) error { return c.SendString("ok") })
	e.RegisterMiddlewareFactory("require_role",
		factory.RequireRole(func(c *fibermap.Context[appCtx]) string { return c.Data.Role }),
	)
	if err := e.LoadBytes([]byte(`
groups:
  - routes:
      - method: GET
        path: /x
        handler: ok
        middleware: [{require_role: []}]
`)); err != nil {
		t.Fatal(err)
	}

	err := e.Mount(fiber.New())
	if err == nil {
		t.Fatal("expected mount error for empty args")
	}
	var fe *fibermap.Error
	if !errors.As(err, &fe) || fe.Code != fibermap.CodeInvalidFactoryArgs {
		t.Errorf("err = %v, want CodeInvalidFactoryArgs", err)
	}
}

func TestRequireRole_CustomDenyHandler(t *testing.T) {
	app := buildEngine(t, `
groups:
  - routes:
      - method: GET
        path: /x
        handler: ok
        middleware: [{require_role: [director]}]
`, func(e *fibermap.Engine[appCtx]) {
		e.RegisterMiddlewareFactory("require_role",
			factory.RequireRole(
				func(c *fibermap.Context[appCtx]) string { return c.Data.Role },
				factory.WithDenyHandler(func(c *fiber.Ctx) error {
					return c.Status(401).SendString("custom-deny")
				}),
			),
		)
	}, "guest", nil)

	resp, _ := app.Test(httptest.NewRequest("GET", "/x", nil))
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 401 || string(body) != "custom-deny" {
		t.Errorf("status=%d body=%q", resp.StatusCode, string(body))
	}
}

func TestRequireAnyScope_Allowed(t *testing.T) {
	app := buildEngine(t, `
groups:
  - routes:
      - method: GET
        path: /x
        handler: ok
        middleware: [{require_scope: ["tasks:write", "tasks:admin"]}]
`, func(e *fibermap.Engine[appCtx]) {
		e.RegisterMiddlewareFactory("require_scope",
			factory.RequireAnyScope(func(c *fibermap.Context[appCtx]) []string { return c.Data.Scopes }),
		)
	}, "", []string{"tasks:write"})

	resp, _ := app.Test(httptest.NewRequest("GET", "/x", nil))
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRequireAnyScope_Denied(t *testing.T) {
	app := buildEngine(t, `
groups:
  - routes:
      - method: GET
        path: /x
        handler: ok
        middleware: [{require_scope: ["tasks:admin"]}]
`, func(e *fibermap.Engine[appCtx]) {
		e.RegisterMiddlewareFactory("require_scope",
			factory.RequireAnyScope(func(c *fibermap.Context[appCtx]) []string { return c.Data.Scopes }),
		)
	}, "", []string{"tasks:read"})

	resp, _ := app.Test(httptest.NewRequest("GET", "/x", nil))
	if resp.StatusCode != fiber.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestAdapter_RunsFiberHandler(t *testing.T) {
	called := 0
	mw := func(c *fiber.Ctx) error {
		called++
		c.Set("X-Adapter", "yes")
		return c.Next()
	}

	app := buildEngine(t, `
groups:
  - middleware: [tag]
    routes:
      - { method: GET, path: /x, handler: ok }
`, func(e *fibermap.Engine[appCtx]) {
		e.RegisterMiddleware("tag", factory.Adapter[appCtx](mw))
	}, "", nil)

	resp, _ := app.Test(httptest.NewRequest("GET", "/x", nil))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if called != 1 {
		t.Errorf("adapter mw called %d times, want 1", called)
	}
	if got := resp.Header.Get("X-Adapter"); got != "yes" {
		t.Errorf("X-Adapter = %q, want yes", got)
	}
}

func TestAdapterFactory_Args(t *testing.T) {
	var seen []string
	app := buildEngine(t, `
groups:
  - middleware:
      - {tag: [a, b]}
    routes:
      - { method: GET, path: /x, handler: ok }
`, func(e *fibermap.Engine[appCtx]) {
		e.RegisterMiddlewareFactory("tag", factory.AdapterFactory[appCtx](
			func(args []string) (fiber.Handler, error) {
				seen = append([]string(nil), args...)
				return func(c *fiber.Ctx) error { return c.Next() }, nil
			},
		))
	}, "", nil)

	if _, err := app.Test(httptest.NewRequest("GET", "/x", nil)); err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "b"}
	if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Errorf("factory got %v, want %v", seen, want)
	}
}

func TestAdapterFactory_ErrorSurfaces(t *testing.T) {
	e := fibermap.New[appCtx]()
	e.SetContextBuilder(func(c *fiber.Ctx) (appCtx, error) { return appCtx{}, nil })
	e.RegisterHandler("ok", func(c *fibermap.Context[appCtx]) error { return c.SendString("ok") })
	e.RegisterMiddlewareFactory("tag", factory.AdapterFactory[appCtx](
		func(args []string) (fiber.Handler, error) {
			return nil, errors.New("nope")
		},
	))
	if err := e.LoadBytes([]byte(`
groups:
  - middleware: [{tag: [a]}]
    routes:
      - { method: GET, path: /x, handler: ok }
`)); err != nil {
		t.Fatal(err)
	}

	err := e.Mount(fiber.New())
	if err == nil {
		t.Fatal("expected mount error")
	}
	var fe *fibermap.Error
	if !errors.As(err, &fe) || fe.Code != fibermap.CodeInvalidFactoryArgs {
		t.Errorf("err = %v, want CodeInvalidFactoryArgs", err)
	}
}
