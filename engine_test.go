package fibermap

import (
	"errors"
	"path/filepath"
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
