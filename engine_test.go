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

// expectRegisterPanic runs fn and returns the recovered *Error, or fails
// the test if no panic happened or the panic value wasn't *Error.
func expectRegisterPanic(t *testing.T, fn func()) *Error {
	t.Helper()
	defer func() {}()
	var got *Error
	func() {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			fe, ok := r.(*Error)
			if !ok {
				t.Fatalf("recovered non-*Error: %T %v", r, r)
			}
			got = fe
		}()
		fn()
	}()
	if got == nil {
		t.Fatal("expected panic, got none")
	}
	return got
}

func TestEngine_RegisterHandler_Duplicate(t *testing.T) {
	e := newTestEngine()
	h := func(c *Context[engCtx]) error { return nil }

	e.RegisterHandler("x", h)
	fe := expectRegisterPanic(t, func() { e.RegisterHandler("x", h) })
	if fe.Code != CodeDuplicateRegistration {
		t.Errorf("code = %q", fe.Code)
	}
}

func TestEngine_RegisterMiddleware_Duplicate(t *testing.T) {
	e := newTestEngine()
	m := func(c *Context[engCtx]) error { return c.Next() }

	e.RegisterMiddleware("auth", m)
	fe := expectRegisterPanic(t, func() { e.RegisterMiddleware("auth", m) })
	if fe.Code != CodeDuplicateRegistration {
		t.Errorf("code = %q", fe.Code)
	}
}

func TestEngine_RegisterMiddleware_ConflictsWithFactory(t *testing.T) {
	e := newTestEngine()
	e.RegisterMiddlewareFactory("require_role", func(args []string) (MiddlewareFunc[engCtx], error) {
		return func(c *Context[engCtx]) error { return c.Next() }, nil
	})
	fe := expectRegisterPanic(t, func() {
		e.RegisterMiddleware("require_role", func(c *Context[engCtx]) error { return c.Next() })
	})
	if fe.Code != CodeDuplicateRegistration {
		t.Errorf("code = %q", fe.Code)
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

func TestMount_DuplicateRoute(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("a.get", func(c *Context[engCtx]) error { return nil })
	e.RegisterHandler("b.get", func(c *Context[engCtx]) error { return nil })
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
	e.RegisterHandler("x.get", func(c *Context[engCtx]) error { return nil })
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
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error {
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
	e.RegisterMiddleware("logger", func(c *Context[engCtx]) error {
		calls = append(calls, "logger")
		return c.Next()
	})
	e.RegisterMiddleware("auth", func(c *Context[engCtx]) error {
		calls = append(calls, "auth")
		return c.Next()
	})
	e.RegisterMiddleware("authorized", func(c *Context[engCtx]) error {
		calls = append(calls, "authorized")
		return c.Next()
	})
	e.RegisterHandler("user.me", func(c *Context[engCtx]) error {
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

func registerRoleFactory(e *Engine[engCtx]) {
	e.RegisterMiddlewareFactory("require_role", func(args []string) (MiddlewareFunc[engCtx], error) {
		allowed := append([]string(nil), args...)
		return func(c *Context[engCtx]) error {
			for _, a := range allowed {
				if a == c.Data.Role {
					return c.Next()
				}
			}
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "forbidden"})
		}, nil
	})
}

func TestEngine_EndToEnd_FactoryAllowed(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) {
		return engCtx{Role: "admin"}, nil
	})
	registerRoleFactory(e)
	e.RegisterMiddleware("auth", func(c *Context[engCtx]) error { return c.Next() })
	e.RegisterHandler("x.create", func(c *Context[engCtx]) error { return c.SendString("created") })
	_ = e.LoadFile(filepath.Join("testdata", "factories.yaml"))

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	resp, _ := app.Test(httptest.NewRequest("POST", "/v1/create", nil))
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestEngine_EndToEnd_FactoryDenied(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) {
		return engCtx{Role: "doctor"}, nil
	})
	registerRoleFactory(e)
	e.RegisterMiddleware("auth", func(c *Context[engCtx]) error { return c.Next() })
	e.RegisterHandler("x.create", func(c *Context[engCtx]) error {
		t.Fatal("handler should not be reached")
		return nil
	})
	_ = e.LoadFile(filepath.Join("testdata", "factories.yaml"))

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	resp, _ := app.Test(httptest.NewRequest("POST", "/v1/create", nil))
	if resp.StatusCode != fiber.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestEngine_FactoryArgsRejected(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterMiddleware("auth", func(c *Context[engCtx]) error { return c.Next() })
	e.RegisterMiddlewareFactory("require_role", func(args []string) (MiddlewareFunc[engCtx], error) {
		if len(args) == 0 {
			return nil, errors.New("at least one role required")
		}
		return func(c *Context[engCtx]) error { return c.Next() }, nil
	})
	e.RegisterHandler("x.create", func(c *Context[engCtx]) error { return nil })
	_ = e.LoadBytes([]byte(`
groups:
  - prefix: /v1
    middleware: [auth]
    routes:
      - method: POST
        path: /create
        handler: x.create
        middleware:
          - require_role: []
`))

	app := fiber.New()
	err := e.Mount(app)
	if !containsCode(err, CodeInvalidFactoryArgs) {
		t.Errorf("want CodeInvalidFactoryArgs, got %v", err)
	}
}

func TestEngine_FactoryRegisteredAsPlain_MountError(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	registerRoleFactory(e)
	e.RegisterMiddleware("auth", func(c *Context[engCtx]) error { return c.Next() })
	e.RegisterHandler("x.create", func(c *Context[engCtx]) error { return nil })
	_ = e.LoadBytes([]byte(`
groups:
  - prefix: /v1
    middleware: [auth, require_role]
    routes:
      - { method: POST, path: /create, handler: x.create }
`))

	app := fiber.New()
	err := e.Mount(app)
	if !containsCode(err, CodeUnknownMiddleware) {
		t.Errorf("want CodeUnknownMiddleware (factory used as scalar), got %v", err)
	}
}

func TestEngine_EndToEnd_ContextBuilderError(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) {
		return engCtx{}, errors.New("no locals")
	})
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error {
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
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return nil })
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
	registerRoleFactory(e)
	e.RegisterMiddleware("auth", func(c *Context[engCtx]) error { return c.Next() })
	e.RegisterHandler("x.create", func(c *Context[engCtx]) error { return nil })
	_ = e.LoadFile(filepath.Join("testdata", "factories.yaml"))

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
	if len(r.Middleware) != 2 {
		t.Fatalf("middleware len = %d, want 2", len(r.Middleware))
	}
	if r.Middleware[0].Name != "auth" || len(r.Middleware[0].Args) != 0 {
		t.Errorf("mw[0] = %+v", r.Middleware[0])
	}
	if r.Middleware[1].Name != "require_role" || len(r.Middleware[1].Args) != 1 || r.Middleware[1].Args[0] != "admin" {
		t.Errorf("mw[1] = %+v", r.Middleware[1])
	}
}

func TestEngine_Routes_BeforeMount(t *testing.T) {
	e := newTestEngine()
	if rs := e.Routes(); len(rs) != 0 {
		t.Errorf("routes before mount = %d, want 0", len(rs))
	}
}

func TestEngine_Routes_IsDefensiveCopy(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	registerRoleFactory(e)
	e.RegisterMiddleware("auth", func(c *Context[engCtx]) error { return c.Next() })
	e.RegisterHandler("x.create", func(c *Context[engCtx]) error { return nil })
	_ = e.LoadFile(filepath.Join("testdata", "factories.yaml"))

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	rs1 := e.Routes()
	rs1[0].Middleware[0].Name = "MUTATED"
	rs1[0].Middleware[1].Args[0] = "MUTATED"

	rs2 := e.Routes()
	if rs2[0].Middleware[0].Name != "auth" {
		t.Errorf("Middleware.Name was mutated through Routes() snapshot")
	}
	if rs2[0].Middleware[1].Args[0] != "admin" {
		t.Errorf("Middleware.Args was mutated through Routes() snapshot")
	}
}
