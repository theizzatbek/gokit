package sse_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/fibermap/sse"
)

type appCtx struct{ Subject string }

// mount wires the engine with one SSE handler under `name` and
// mounts it at the supplied YAML route. Returns the running
// fiber.App so callers can app.Test() against it.
func mount(t *testing.T, name, path string, fn sse.HandlerFunc[appCtx]) *fiber.App {
	t.Helper()
	app := fiber.New()
	eng := fibermap.New[appCtx]()
	eng.SetContextBuilder(func(c *fiber.Ctx) (appCtx, error) {
		return appCtx{Subject: "u-1"}, nil
	})
	sse.Register(eng, name, fn)
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

// hitSSE issues a GET against `path` and returns the full response
// body as a string.
func hitSSE(t *testing.T, app *fiber.App, path string) string {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	resp, err := app.Test(req, -1) // -1 = no internal timeout
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestSSE_Send_BasicFrame(t *testing.T) {
	app := mount(t, "events.basic", "/events/basic",
		func(_ context.Context, _ *fibermap.Context[appCtx], s *sse.Stream) error {
			_ = s.Send("hello", "world")
			return nil
		})
	body := hitSSE(t, app, "/events/basic")
	if !strings.Contains(body, "event: hello\n") {
		t.Errorf("missing event line in body:\n%s", body)
	}
	if !strings.Contains(body, "data: world\n") {
		t.Errorf("missing data line in body:\n%s", body)
	}
}

func TestSSE_SendJSON(t *testing.T) {
	type payload struct {
		Foo string `json:"foo"`
	}
	app := mount(t, "events.json", "/events/json",
		func(_ context.Context, _ *fibermap.Context[appCtx], s *sse.Stream) error {
			_ = s.SendJSON("update", payload{Foo: "bar"})
			return nil
		})
	body := hitSSE(t, app, "/events/json")
	var got payload
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") {
			raw := strings.TrimPrefix(line, "data: ")
			if err := json.Unmarshal([]byte(raw), &got); err != nil {
				t.Fatalf("unmarshal %q: %v", raw, err)
			}
		}
	}
	if got.Foo != "bar" {
		t.Errorf("got %+v, want {foo:bar}", got)
	}
}

func TestSSE_Comment_KeepAlive(t *testing.T) {
	app := mount(t, "events.keep", "/events/keep",
		func(_ context.Context, _ *fibermap.Context[appCtx], s *sse.Stream) error {
			return s.Comment("ping")
		})
	body := hitSSE(t, app, "/events/keep")
	if !strings.Contains(body, ": ping\n\n") {
		t.Errorf("missing comment frame in body:\n%s", body)
	}
}

func TestSSE_MultilineDataSplits(t *testing.T) {
	app := mount(t, "events.multi", "/events/multi",
		func(_ context.Context, _ *fibermap.Context[appCtx], s *sse.Stream) error {
			return s.Send("", "line1\nline2\nline3")
		})
	body := hitSSE(t, app, "/events/multi")
	for _, want := range []string{"data: line1\n", "data: line2\n", "data: line3\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in body:\n%s", want, body)
		}
	}
	if strings.Contains(body, "event: ") {
		t.Error("empty event field should not emit `event:` line")
	}
}

func TestSSE_HeadersSet(t *testing.T) {
	app := mount(t, "events.hdr", "/events/hdr",
		func(_ context.Context, _ *fibermap.Context[appCtx], s *sse.Stream) error {
			return s.Send("ok", "1")
		})
	req := httptest.NewRequest("GET", "/events/hdr", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "no-cache") {
		t.Errorf("Cache-Control = %q, want contains no-cache", got)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}
}

func TestSSE_NilEngine_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil engine")
		}
	}()
	sse.Register[appCtx](nil, "x", nil)
}

func TestSSE_NilHandler_Panics(t *testing.T) {
	eng := fibermap.New[appCtx]()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil handler")
		}
	}()
	sse.Register[appCtx](eng, "x", nil)
}
