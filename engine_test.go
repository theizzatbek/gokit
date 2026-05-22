package fibermap

import (
	"errors"
	"io"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gofiber/fiber/v2"
)

type engCtx struct {
	UserID string
	Role   string
}

func newTestEngine() *Engine[engCtx] {
	return New[engCtx]()
}

func TestEngine_RegisterHandler_Duplicate(t *testing.T) {
	e := newTestEngine()
	h := func(c *Context[engCtx]) error { return nil }

	if err := e.RegisterHandler("x", h); err != nil {
		t.Fatalf("first register: %v", err)
	}
	err := e.RegisterHandler("x", h)
	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if fe.Code != CodeDuplicateRegistration {
		t.Errorf("code = %q", fe.Code)
	}
}

func TestEngine_RegisterMiddleware_Duplicate(t *testing.T) {
	e := newTestEngine()
	m := func(c *Context[engCtx]) error { return c.Next() }

	if err := e.RegisterMiddleware("auth", m); err != nil {
		t.Fatal(err)
	}
	if err := e.RegisterMiddleware("auth", m); err == nil {
		t.Errorf("want duplicate error")
	}
}

func TestEngine_SetOverwritesSilently(t *testing.T) {
	e := newTestEngine()
	called := 0

	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { called = 1; return engCtx{}, nil })
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { called = 2; return engCtx{}, nil })

	// We can't easily invoke from outside; just ensure no panic and second wins via internal state.
	if e.builder == nil {
		t.Fatal("builder should be set")
	}
	_, _ = e.builder(nil)
	if called != 2 {
		t.Errorf("called = %d, want 2", called)
	}
}

func TestMount_MissingContextBuilder(t *testing.T) {
	e := newTestEngine()
	_ = e.LoadFile(filepath.Join("testdata", "basic.yaml"))

	app := fiber.New()
	err := e.Mount(app)

	if !containsCode(err, CodeMissingContextBuilder) {
		t.Errorf("want CodeMissingContextBuilder, got %v", err)
	}
}

func TestMount_UnknownHandler(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	_ = e.LoadFile(filepath.Join("testdata", "basic.yaml"))

	app := fiber.New()
	err := e.Mount(app)

	if !containsCode(err, CodeUnknownHandler) {
		t.Errorf("want CodeUnknownHandler, got %v", err)
	}
}

func TestMount_RolesWithoutChecker(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	_ = e.RegisterMiddleware("auth", func(c *Context[engCtx]) error { return c.Next() })
	_ = e.RegisterHandler("x.create", func(c *Context[engCtx]) error { return nil })
	_ = e.LoadFile(filepath.Join("testdata", "roles.yaml"))

	app := fiber.New()
	err := e.Mount(app)

	if !containsCode(err, CodeMissingRoleChecker) {
		t.Errorf("want CodeMissingRoleChecker, got %v", err)
	}
}

func TestMount_DuplicateRoute(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	_ = e.RegisterHandler("a.get", func(c *Context[engCtx]) error { return nil })
	_ = e.RegisterHandler("b.get", func(c *Context[engCtx]) error { return nil })
	_ = e.LoadFile(filepath.Join("testdata", "duplicate_routes.yaml"))

	app := fiber.New()
	err := e.Mount(app)

	if !containsCode(err, CodeDuplicateRoute) {
		t.Errorf("want CodeDuplicateRoute, got %v", err)
	}
}

func TestMount_AccumulatesErrors(t *testing.T) {
	e := newTestEngine()
	// no ContextBuilder, no handler — should produce both errors at once.
	_ = e.LoadFile(filepath.Join("testdata", "basic.yaml"))

	app := fiber.New()
	err := e.Mount(app)

	if !containsCode(err, CodeMissingContextBuilder) || !containsCode(err, CodeUnknownHandler) {
		t.Errorf("expected both errors in joined result: %v", err)
	}
}

func TestMount_UnknownMiddlewareSet(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	_ = e.RegisterHandler("x.get", func(c *Context[engCtx]) error { return nil })
	_ = e.LoadBytes([]byte(`
groups:
  - prefix: /v1
    middleware_set: nope_set
    routes:
      - { method: GET, path: /x, handler: x.get }
`))

	app := fiber.New()
	err := e.Mount(app)

	if !containsCode(err, CodeUnknownMiddlewareSet) {
		t.Errorf("want CodeUnknownMiddlewareSet, got %v", err)
	}
}

// containsCode returns true if err (possibly errors.Join wrapping multiple
// *Error values) contains any *Error with the given code.
func containsCode(err error, code string) bool {
	if err == nil {
		return false
	}
	type unwrapper interface{ Unwrap() []error }
	if u, ok := err.(unwrapper); ok {
		for _, sub := range u.Unwrap() {
			if containsCode(sub, code) {
				return true
			}
		}
		return false
	}
	var fe *Error
	if errors.As(err, &fe) && fe.Code == code {
		return true
	}
	return false
}

func TestEngine_EndToEnd_BasicRoute(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) {
		return engCtx{UserID: "u1", Role: "doctor"}, nil
	})
	_ = e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error {
		return c.Status(200).SendString("pong " + c.Data.UserID)
	})
	_ = e.LoadFile(filepath.Join("testdata", "basic.yaml"))

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	resp, err := app.Test(httptest.NewRequest("GET", "/v1/ping", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "pong u1" {
		t.Errorf("status=%d body=%q", resp.StatusCode, string(body))
	}
}

func TestEngine_EndToEnd_MiddlewareOrder(t *testing.T) {
	e := newTestEngine()
	var calls []string
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) {
		calls = append(calls, "ctx")
		return engCtx{Role: "admin"}, nil
	})
	_ = e.RegisterMiddleware("logger", func(c *Context[engCtx]) error {
		calls = append(calls, "logger")
		return c.Next()
	})
	_ = e.RegisterMiddleware("auth", func(c *Context[engCtx]) error {
		calls = append(calls, "auth")
		return c.Next()
	})
	_ = e.RegisterMiddleware("authorized", func(c *Context[engCtx]) error {
		calls = append(calls, "authorized")
		return c.Next()
	})
	_ = e.RegisterHandler("user.me", func(c *Context[engCtx]) error {
		calls = append(calls, "handler")
		return c.SendString("ok")
	})
	_ = e.LoadFile(filepath.Join("testdata", "middleware_sets.yaml"))

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	_, err := app.Test(httptest.NewRequest("GET", "/v1/me", nil))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"ctx", "logger", "auth", "authorized", "handler"}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

func TestEngine_EndToEnd_RoleGuard_Allowed(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) {
		return engCtx{Role: "admin"}, nil
	})
	e.SetRoleChecker(func(c *Context[engCtx], allowed []string) bool {
		for _, r := range allowed {
			if r == c.Data.Role {
				return true
			}
		}
		return false
	})
	_ = e.RegisterMiddleware("auth", func(c *Context[engCtx]) error { return c.Next() })
	_ = e.RegisterHandler("x.create", func(c *Context[engCtx]) error { return c.SendString("created") })
	_ = e.LoadFile(filepath.Join("testdata", "roles.yaml"))

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	resp, _ := app.Test(httptest.NewRequest("POST", "/v1/create", nil))
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestEngine_EndToEnd_RoleGuard_Denied(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) {
		return engCtx{Role: "doctor"}, nil
	})
	e.SetRoleChecker(func(c *Context[engCtx], allowed []string) bool {
		for _, r := range allowed {
			if r == c.Data.Role {
				return true
			}
		}
		return false
	})
	_ = e.RegisterMiddleware("auth", func(c *Context[engCtx]) error { return c.Next() })
	_ = e.RegisterHandler("x.create", func(c *Context[engCtx]) error {
		t.Fatal("handler should not be reached")
		return nil
	})
	_ = e.LoadFile(filepath.Join("testdata", "roles.yaml"))

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	resp, _ := app.Test(httptest.NewRequest("POST", "/v1/create", nil))
	if resp.StatusCode != fiber.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestEngine_EndToEnd_ContextBuilderError(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) {
		return engCtx{}, errors.New("no locals")
	})
	_ = e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error {
		t.Fatal("handler should not be reached")
		return nil
	})
	_ = e.LoadFile(filepath.Join("testdata", "basic.yaml"))

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	resp, _ := app.Test(httptest.NewRequest("GET", "/v1/ping", nil))
	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestEngine_MountTwice(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	_ = e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return nil })
	_ = e.LoadFile(filepath.Join("testdata", "basic.yaml"))

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}
	err := e.Mount(app)
	if !containsCode(err, CodeAlreadyMounted) {
		t.Errorf("want CodeAlreadyMounted, got %v", err)
	}
}

func TestEngine_Routes_AfterMount(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.SetRoleChecker(func(c *Context[engCtx], allowed []string) bool { return true })
	_ = e.RegisterMiddleware("auth", func(c *Context[engCtx]) error { return c.Next() })
	_ = e.RegisterHandler("x.create", func(c *Context[engCtx]) error { return nil })
	_ = e.LoadFile(filepath.Join("testdata", "roles.yaml"))

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	rs := e.Routes()
	if len(rs) != 1 {
		t.Fatalf("routes = %d, want 1", len(rs))
	}
	r := rs[0]
	if r.Method != "POST" || r.Path != "/v1/create" || r.Handler != "x.create" {
		t.Errorf("route = %+v", r)
	}
	if !reflect.DeepEqual(r.Roles, []string{"admin"}) {
		t.Errorf("roles = %v", r.Roles)
	}
	if !reflect.DeepEqual(r.Middleware, []string{"auth"}) {
		t.Errorf("middleware = %v", r.Middleware)
	}
}

func TestEngine_Routes_BeforeMount(t *testing.T) {
	e := newTestEngine()
	if rs := e.Routes(); len(rs) != 0 {
		t.Errorf("routes before mount = %d, want 0", len(rs))
	}
}
