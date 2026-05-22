package fibermaptest_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/fibermap"
	"github.com/theizzatbek/fibermap/fibermaptest"
)

type ctx struct{ UserID string }

// fakeTB captures Errorf calls so the tests can assert what
// fibermaptest reports without actually failing themselves.
type fakeTB struct {
	errs []string
}

func (f *fakeTB) Helper() {}
func (f *fakeTB) Errorf(format string, args ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}
func (f *fakeTB) joined() string { return strings.Join(f.errs, "\n") }

func buildEng(t *testing.T) *fibermap.Engine[ctx] {
	t.Helper()
	e := fibermap.New[ctx]()
	e.SetContextBuilder(func(c *fiber.Ctx) (ctx, error) { return ctx{}, nil })
	e.RegisterMiddleware("auth", func(c *fibermap.Context[ctx]) error { return c.Next() })
	e.RegisterMiddleware("audit", func(c *fibermap.Context[ctx]) error { return c.Next() })
	e.RegisterHandler("things.list", func(c *fibermap.Context[ctx]) error { return nil })
	e.RegisterHandler("things.create", func(c *fibermap.Context[ctx]) error { return nil })
	if err := e.LoadBytes([]byte(`
groups:
  - prefix: /v1
    middleware: [auth]
    routes:
      - method: GET
        path: /things
        handler: things.list
        tags: [things, read]
      - method: POST
        path: /things
        handler: things.create
        tags: [things, write]
        middleware: [audit]
`)); err != nil {
		t.Fatal(err)
	}
	if err := e.Mount(fiber.New()); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestAssertRoute_Pass(t *testing.T) {
	e := buildEng(t)
	f := &fakeTB{}
	fibermaptest.AssertRoute(f, e, "GET", "/v1/things",
		fibermaptest.WithHandler("things.list"),
		fibermaptest.WithMiddleware("auth"),
		fibermaptest.WithTags("things", "read"),
	)
	if len(f.errs) != 0 {
		t.Errorf("expected no errors, got: %s", f.joined())
	}
}

func TestAssertRoute_NotFound(t *testing.T) {
	e := buildEng(t)
	f := &fakeTB{}
	fibermaptest.AssertRoute(f, e, "GET", "/missing")
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "no route registered") {
		t.Errorf("want 'no route registered' error, got: %s", f.joined())
	}
}

func TestAssertRoute_WrongHandler(t *testing.T) {
	e := buildEng(t)
	f := &fakeTB{}
	fibermaptest.AssertRoute(f, e, "GET", "/v1/things",
		fibermaptest.WithHandler("wrong.name"))
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "handler") {
		t.Errorf("want handler error, got: %s", f.joined())
	}
}

func TestAssertRoute_MissingMiddleware(t *testing.T) {
	e := buildEng(t)
	f := &fakeTB{}
	fibermaptest.AssertRoute(f, e, "GET", "/v1/things",
		fibermaptest.WithMiddleware("auth", "not-installed"))
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "middleware chain") {
		t.Errorf("want middleware chain error, got: %s", f.joined())
	}
}

func TestAssertRoute_MiddlewareOutOfOrder(t *testing.T) {
	e := buildEng(t)
	f := &fakeTB{}
	// audit comes AFTER auth in chain; asking for the reverse order fails.
	fibermaptest.AssertRoute(f, e, "POST", "/v1/things",
		fibermaptest.WithMiddleware("audit", "auth"))
	if len(f.errs) != 1 {
		t.Errorf("want one error, got %d: %s", len(f.errs), f.joined())
	}
}

func TestAssertRoute_MissingTag(t *testing.T) {
	e := buildEng(t)
	f := &fakeTB{}
	fibermaptest.AssertRoute(f, e, "GET", "/v1/things",
		fibermaptest.WithTags("things", "missing-tag"))
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "tags") {
		t.Errorf("want tags error, got: %s", f.joined())
	}
}

func TestAssertNoRoute_Pass(t *testing.T) {
	e := buildEng(t)
	f := &fakeTB{}
	fibermaptest.AssertNoRoute(f, e, "DELETE", "/v1/things")
	if len(f.errs) != 0 {
		t.Errorf("expected no errors, got: %s", f.joined())
	}
}

func TestAssertNoRoute_Fail(t *testing.T) {
	e := buildEng(t)
	f := &fakeTB{}
	fibermaptest.AssertNoRoute(f, e, "GET", "/v1/things")
	if len(f.errs) != 1 {
		t.Errorf("want one error, got %d", len(f.errs))
	}
}

func TestAssertRouteCount(t *testing.T) {
	e := buildEng(t)

	f := &fakeTB{}
	fibermaptest.AssertRouteCount(f, e, 2)
	if len(f.errs) != 0 {
		t.Errorf("count=2 should pass, got: %s", f.joined())
	}

	f = &fakeTB{}
	fibermaptest.AssertRouteCount(f, e, 5)
	if len(f.errs) != 1 {
		t.Errorf("count=5 should fail with one error, got %d: %s", len(f.errs), f.joined())
	}
}
