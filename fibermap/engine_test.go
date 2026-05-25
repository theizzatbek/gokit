package fibermap

import (
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

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

func TestEngine_Validate_NoYAML(t *testing.T) {
	e := newTestEngine()
	err := e.Validate()
	if !containsCode(err, CodeInvalidYAML) {
		t.Errorf("want CodeInvalidYAML, got %v", err)
	}
}

func TestEngine_Validate_AccumulatesErrors(t *testing.T) {
	e := newTestEngine()
	// no ContextBuilder, no handler — Validate should surface both.
	if err := e.LoadFile(filepath.Join("testdata", "basic.yaml")); err != nil {
		t.Fatal(err)
	}
	err := e.Validate()
	if !containsCode(err, CodeMissingContextBuilder) || !containsCode(err, CodeUnknownHandler) {
		t.Errorf("want both errors, got %v", err)
	}
}

func TestEngine_Validate_DoesNotMount(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return nil })
	if err := e.LoadFile(filepath.Join("testdata", "basic.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// engine was not mounted; Mount must still succeed afterwards.
	if e.mounted {
		t.Fatal("Validate must not flip the mounted flag")
	}
	if len(e.routes) != 0 {
		t.Fatalf("Validate must not populate routes; got %d", len(e.routes))
	}
	if err := e.Mount(fiber.New()); err != nil {
		t.Errorf("Mount after Validate failed: %v", err)
	}
}

func TestEngine_LoadFS(t *testing.T) {
	fsys := fstest.MapFS{
		"routes.yaml": &fstest.MapFile{Data: []byte(`
groups:
  - prefix: /v1
    routes:
      - { method: GET, path: /ping, handler: ping.handle }
`)},
	}
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong") })
	if err := e.LoadFS(fsys, "routes.yaml"); err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	if err := e.Mount(fiber.New()); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if len(e.Routes()) != 1 {
		t.Errorf("routes = %d, want 1", len(e.Routes()))
	}
}

func TestEngine_LoadFS_FileNotFound(t *testing.T) {
	e := newTestEngine()
	err := e.LoadFS(fstest.MapFS{}, "missing.yaml")
	if !containsCode(err, CodeFileNotFound) {
		t.Errorf("want CodeFileNotFound, got %v", err)
	}
}

func TestEngine_RegisterAfterMount_Panics(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return nil })
	if err := e.LoadFile(filepath.Join("testdata", "basic.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := e.Mount(fiber.New()); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		fn   func()
	}{
		{"handler", func() {
			e.RegisterHandler("late.handler", func(c *Context[engCtx]) error { return nil })
		}},
		{"middleware", func() {
			e.RegisterMiddleware("late.mw", func(c *Context[engCtx]) error { return c.Next() })
		}},
		{"factory", func() {
			e.RegisterMiddlewareFactory("late.factory", func(args []string) (MiddlewareFunc[engCtx], error) {
				return func(c *Context[engCtx]) error { return c.Next() }, nil
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fe := expectRegisterPanic(t, tc.fn)
			if fe.Code != CodeRegisterAfterMount {
				t.Errorf("code = %q, want %q", fe.Code, CodeRegisterAfterMount)
			}
		})
	}
}

func TestEngine_Walk(t *testing.T) {
	e := buildEngineWithTwoRoutes(t)

	var seen []string
	if err := e.Walk(func(r RouteInfo) error {
		seen = append(seen, r.Method+" "+r.Path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /v1/a", "GET /v1/b"}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("seen = %v, want %v", seen, want)
	}
}

func TestEngine_Walk_StopWalk(t *testing.T) {
	e := buildEngineWithTwoRoutes(t)
	var seen int
	err := e.Walk(func(r RouteInfo) error {
		seen++
		return ErrStopWalk
	})
	if err != nil {
		t.Errorf("ErrStopWalk should be swallowed, got %v", err)
	}
	if seen != 1 {
		t.Errorf("walked %d routes, want 1 (stopped after first)", seen)
	}
}

func TestEngine_Walk_PropagatesError(t *testing.T) {
	e := buildEngineWithTwoRoutes(t)
	boom := errors.New("boom")
	err := e.Walk(func(r RouteInfo) error { return boom })
	if err != boom {
		t.Errorf("err = %v, want %v", err, boom)
	}
}

func TestEngine_Lookup(t *testing.T) {
	e := buildEngineWithTwoRoutes(t)
	if r, ok := e.Lookup("GET", "/v1/a"); !ok || r.Handler != "h.a" {
		t.Errorf("lookup /v1/a: %+v ok=%v", r, ok)
	}
	if r, ok := e.Lookup("GET", "/missing"); ok {
		t.Errorf("lookup missing: should be false, got %+v", r)
	}
	if _, ok := e.Lookup("POST", "/v1/a"); ok {
		t.Error("lookup wrong method: should be false")
	}
}

func buildEngineWithTwoRoutes(t *testing.T) *Engine[engCtx] {
	t.Helper()
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("h.a", func(c *Context[engCtx]) error { return nil })
	e.RegisterHandler("h.b", func(c *Context[engCtx]) error { return nil })
	if err := e.LoadBytes([]byte(`
groups:
  - prefix: /v1
    routes:
      - { method: GET, path: /a, handler: h.a }
      - { method: GET, path: /b, handler: h.b }
`)); err != nil {
		t.Fatal(err)
	}
	if err := e.Mount(fiber.New()); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestTimeout_InvalidDuration(t *testing.T) {
	e := newTestEngine()
	err := e.LoadFile(filepath.Join("testdata", "invalid_timeout.yaml"))
	if !containsCode(err, CodeInvalidTimeout) {
		t.Errorf("want CodeInvalidTimeout, got %v", err)
	}
}

func TestTimeout_ZeroNotAllowed(t *testing.T) {
	e := newTestEngine()
	err := e.LoadBytes([]byte(`
groups:
  - routes:
      - { method: GET, path: /x, handler: x, timeout: 0s }
`))
	if !containsCode(err, CodeInvalidTimeout) {
		t.Errorf("want CodeInvalidTimeout, got %v", err)
	}
}

func TestTimeout_RouteInfoSurfacesSpec(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("fast", func(c *Context[engCtx]) error { return c.SendString("ok") })
	e.RegisterHandler("slow", func(c *Context[engCtx]) error { return c.SendString("ok") })
	e.RegisterHandler("upload", func(c *Context[engCtx]) error { return c.SendString("ok") })
	if err := e.LoadFile(filepath.Join("testdata", "timeout.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := e.Mount(fiber.New()); err != nil {
		t.Fatal(err)
	}

	if r, _ := e.Lookup("GET", "/v1/fast"); r.Timeout != "" {
		t.Errorf("fast.Timeout = %q, want empty", r.Timeout)
	}
	if r, _ := e.Lookup("GET", "/v1/slow"); r.Timeout != "50ms" {
		t.Errorf("slow.Timeout = %q, want 50ms", r.Timeout)
	}
	if r, _ := e.Lookup("POST", "/v1/upload"); r.Timeout != "1m30s" {
		t.Errorf("upload.Timeout = %q, want 1m30s", r.Timeout)
	}
}

func TestTimeout_FiresOnSlowHandler(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("slow", func(c *Context[engCtx]) error {
		// Wait for deadline; NewWithContext surfaces context.DeadlineExceeded
		// as 408 Request Timeout.
		<-c.UserContext().Done()
		return c.UserContext().Err()
	})
	if err := e.LoadBytes([]byte(`
groups:
  - routes:
      - { method: GET, path: /slow, handler: slow, timeout: 50ms }
`)); err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	resp, err := app.Test(httptest.NewRequest("GET", "/slow", nil), int(time.Second/time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusRequestTimeout {
		t.Errorf("status = %d, want %d", resp.StatusCode, fiber.StatusRequestTimeout)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("request took %v, expected to abort near the 50ms deadline", elapsed)
	}
}

func TestTimeout_PassesThroughFastHandler(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("fast", func(c *Context[engCtx]) error {
		return c.SendString("ok")
	})
	if err := e.LoadBytes([]byte(`
groups:
  - routes:
      - { method: GET, path: /fast, handler: fast, timeout: 1s }
`)); err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	resp, err := app.Test(httptest.NewRequest("GET", "/fast", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestCache_InvalidTTL_FailsLoad(t *testing.T) {
	cases := []struct {
		name, ttl string
	}{
		{"empty", ""},
		{"garbage", "nope"},
		{"zero", "0s"},
		{"negative", "-1s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEngine()
			yaml := "groups:\n  - routes:\n      - { method: GET, path: /x, handler: x, cache: {ttl: \"" + tc.ttl + "\"} }\n"
			err := e.LoadBytes([]byte(yaml))
			if !containsCode(err, CodeInvalidCache) {
				t.Errorf("want CodeInvalidCache, got %v", err)
			}
		})
	}
}

func TestCache_ScalarForm(t *testing.T) {
	var hits int32
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("counter", func(c *Context[engCtx]) error {
		hits++
		return c.SendString("ok")
	})
	if err := e.LoadBytes([]byte(`
groups:
  - routes:
      - { method: GET, path: /x, handler: counter, cache: 1m }
`)); err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if _, err := app.Test(httptest.NewRequest("GET", "/x", nil)); err != nil {
			t.Fatal(err)
		}
	}
	if hits != 1 {
		t.Errorf("handler invoked %d times, want 1 (cached)", hits)
	}
}

func TestCache_KeyByIsolatesByData(t *testing.T) {
	var hits int32
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) {
		return engCtx{UserID: c.Get("X-User")}, nil
	})
	e.SetCacheDefaults(CacheDefaults[engCtx]{
		KeyBy: func(c *Context[engCtx]) string { return c.Data.UserID },
	})
	e.RegisterHandler("counter", func(c *Context[engCtx]) error {
		hits++
		return c.SendString("ok " + c.Data.UserID)
	})
	if err := e.LoadBytes([]byte(`
groups:
  - routes:
      - { method: GET, path: /x, handler: counter, cache: 1m }
`)); err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	mk := func(user string) {
		req := httptest.NewRequest("GET", "/x", nil)
		req.Header.Set("X-User", user)
		if _, err := app.Test(req); err != nil {
			t.Fatal(err)
		}
	}
	mk("alice")
	mk("alice")
	mk("bob")
	mk("bob")
	if hits != 2 {
		t.Errorf("handler invoked %d times, want 2 (one per user)", hits)
	}
}

func TestCache_VaryHeaderIsolatesByHeader(t *testing.T) {
	var hits int32
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("counter", func(c *Context[engCtx]) error {
		hits++
		return c.SendString("ok")
	})
	if err := e.LoadBytes([]byte(`
groups:
  - routes:
      - method: GET
        path: /x
        handler: counter
        cache:
          ttl: 1m
          vary_header: [Accept-Language]
`)); err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	mk := func(lang string) {
		req := httptest.NewRequest("GET", "/x", nil)
		req.Header.Set("Accept-Language", lang)
		if _, err := app.Test(req); err != nil {
			t.Fatal(err)
		}
	}
	mk("en")
	mk("en")
	mk("ru")
	mk("ru")
	if hits != 2 {
		t.Errorf("handler invoked %d times, want 2 (one per language)", hits)
	}
}

func TestCache_ControlFlag_BypassesOnNoStore(t *testing.T) {
	var hits int32
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("counter", func(c *Context[engCtx]) error {
		hits++
		return c.SendString("ok")
	})
	if err := e.LoadBytes([]byte(`
groups:
  - routes:
      - method: GET
        path: /x
        handler: counter
        cache: {ttl: 1m, control: true}
`)); err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	if _, err := app.Test(httptest.NewRequest("GET", "/x", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Test(httptest.NewRequest("GET", "/x", nil)); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("after 2 vanilla requests, hits = %d, want 1 (cached)", hits)
	}
	bypass := httptest.NewRequest("GET", "/x", nil)
	bypass.Header.Set("Cache-Control", "no-store")
	if _, err := app.Test(bypass); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Errorf("no-store hit count = %d, want 2", hits)
	}
}

func TestCache_RouteInfoSurfacesSpec(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("h", func(c *Context[engCtx]) error { return c.SendString("ok") })
	if err := e.LoadBytes([]byte(`
groups:
  - routes:
      - method: GET
        path: /scalar
        handler: h
        cache: 30s
      - method: GET
        path: /full
        handler: h
        cache:
          ttl: 5m
          control: true
          headers: true
          vary_header: [Accept-Language, Accept-Encoding]
`)); err != nil {
		t.Fatal(err)
	}
	if err := e.Mount(fiber.New()); err != nil {
		t.Fatal(err)
	}

	if r, _ := e.Lookup("GET", "/scalar"); r.Cache == nil || r.Cache.TTL != "30s" {
		t.Errorf("scalar Cache = %+v, want TTL=30s", r.Cache)
	}
	r, _ := e.Lookup("GET", "/full")
	if r.Cache == nil {
		t.Fatal("full Cache = nil")
	}
	if r.Cache.TTL != "5m" || !r.Cache.Control || !r.Cache.Headers {
		t.Errorf("full Cache = %+v", r.Cache)
	}
	if len(r.Cache.VaryHeader) != 2 || r.Cache.VaryHeader[0] != "Accept-Language" {
		t.Errorf("vary_header = %v", r.Cache.VaryHeader)
	}
}

func TestAdd_RoutesServeWithTypedContext(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) {
		return engCtx{UserID: "u-123", Role: "admin"}, nil
	})
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong") })

	// Programmatic — handler receives the typed Context[T].
	e.Add("GET", "/debug/whoami", "debug.whoami", func(c *Context[engCtx]) error {
		return c.SendString(c.Data.UserID + "/" + c.Data.Role)
	}, AddOpts{
		Description: "Echo the current caller's identity",
		Tags:        []string{"debug", "ops"},
	})

	if err := e.LoadFile(filepath.Join("testdata", "basic.yaml")); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	// YAML route still works.
	if resp, _ := app.Test(httptest.NewRequest("GET", "/v1/ping", nil)); resp.StatusCode != 200 {
		t.Errorf("yaml /v1/ping status = %d", resp.StatusCode)
	}

	// Programmatic route works AND its handler sees Context[T].Data.
	resp, _ := app.Test(httptest.NewRequest("GET", "/debug/whoami", nil))
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "u-123/admin" {
		t.Errorf("programmatic status=%d body=%q", resp.StatusCode, string(body))
	}
}

func TestAdd_ShowsInRoutesWithSource(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong") })
	e.Add("GET", "/dbg", "dbg.foo", func(c *Context[engCtx]) error { return c.SendString("x") },
		AddOpts{Tags: []string{"debug"}})

	if err := e.LoadFile(filepath.Join("testdata", "basic.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := e.Mount(fiber.New()); err != nil {
		t.Fatal(err)
	}

	yamlSeen, progSeen := false, false
	for _, r := range e.Routes() {
		switch r.Source {
		case SourceYAML:
			yamlSeen = true
		case SourceProgrammatic:
			progSeen = true
			if r.Path != "/dbg" || r.Handler != "dbg.foo" {
				t.Errorf("programmatic info wrong: %+v", r)
			}
			if len(r.Tags) != 1 || r.Tags[0] != "debug" {
				t.Errorf("programmatic tags = %v", r.Tags)
			}
		default:
			t.Errorf("unexpected source %q on %+v", r.Source, r)
		}
	}
	if !yamlSeen {
		t.Error("no yaml routes saw Source=yaml")
	}
	if !progSeen {
		t.Error("programmatic route missing from Routes()")
	}
}

func TestAdd_PanicsAfterMount(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return nil })
	if err := e.LoadFile(filepath.Join("testdata", "basic.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := e.Mount(fiber.New()); err != nil {
		t.Fatal(err)
	}

	fe := expectRegisterPanic(t, func() {
		e.Add("GET", "/late", "late", func(c *Context[engCtx]) error { return nil })
	})
	if fe.Code != CodeRegisterAfterMount {
		t.Errorf("code = %q, want CodeRegisterAfterMount", fe.Code)
	}
}

func TestAdd_PanicsOnInvalidMethod(t *testing.T) {
	e := newTestEngine()
	fe := expectRegisterPanic(t, func() {
		e.Add("FOO", "/x", "x", func(c *Context[engCtx]) error { return nil })
	})
	if fe.Code != CodeInvalidHTTPMethod {
		t.Errorf("code = %q", fe.Code)
	}
}

func TestAdd_PanicsOnMissingFields(t *testing.T) {
	e := newTestEngine()
	cases := []struct {
		label string
		fn    func()
	}{
		{"empty name", func() { e.Add("GET", "/x", "", func(c *Context[engCtx]) error { return nil }) }},
		{"empty path", func() { e.Add("GET", "", "x", func(c *Context[engCtx]) error { return nil }) }},
		{"nil handler", func() { e.Add("GET", "/x", "x", nil) }},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			fe := expectRegisterPanic(t, tc.fn)
			if fe.Code != CodeMissingField {
				t.Errorf("code = %q, want CodeMissingField", fe.Code)
			}
		})
	}
}

func TestEngine_All_RangeOverFunc(t *testing.T) {
	e := buildEngineWithTwoRoutes(t)

	var collected []string
	for r := range e.All() {
		collected = append(collected, r.Method+" "+r.Path)
	}

	want := []string{"GET /v1/a", "GET /v1/b"}
	if !reflect.DeepEqual(collected, want) {
		t.Errorf("got %v, want %v", collected, want)
	}
}

func TestEngine_All_BreakStops(t *testing.T) {
	e := buildEngineWithTwoRoutes(t)

	count := 0
	for r := range e.All() {
		count++
		if r.Path == "/v1/a" {
			break
		}
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (break should stop after first)", count)
	}
}

func TestEngine_All_DefensiveCopy(t *testing.T) {
	e := buildEngineWithTwoRoutes(t)

	for r := range e.All() {
		r.Tags = append(r.Tags, "mutated")
		_ = r
	}

	for _, r := range e.Routes() {
		for _, tag := range r.Tags {
			if tag == "mutated" {
				t.Errorf("mutation leaked into engine state: %+v", r)
			}
		}
	}
}

func benchEngine(b *testing.B, n int) *Engine[engCtx] {
	b.Helper()
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	for i := 0; i < n; i++ {
		name := "h" + strconv.Itoa(i)
		e.RegisterHandler(name, func(c *Context[engCtx]) error { return nil })
	}

	var sb strings.Builder
	sb.WriteString("groups:\n  - prefix: /v1\n    routes:\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "      - { method: GET, path: /r%d, handler: h%d }\n", i, i)
	}
	if err := e.LoadBytes([]byte(sb.String())); err != nil {
		b.Fatal(err)
	}
	if err := e.Mount(fiber.New()); err != nil {
		b.Fatal(err)
	}
	return e
}

func BenchmarkEngine_Lookup(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		b.Run("routes="+strconv.Itoa(n), func(b *testing.B) {
			e := benchEngine(b, n)
			// Pick a route in the MIDDLE of the slice — worst case
			// for a linear scan.
			method, path := "GET", "/v1/r"+strconv.Itoa(n/2)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, ok := e.Lookup(method, path); !ok {
					b.Fatal("lookup miss")
				}
			}
		})
	}
}
