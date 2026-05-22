package fibermap

import (
	"errors"
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
