package fibermap

import (
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gofiber/fiber/v2"
)

// freeListenAddr binds :0 just long enough to grab a free port and
// then closes the listener, returning an addr fiber can immediately
// Listen on. Avoids port collisions in parallel tests.
func freeListenAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// runAndWait starts Run in a goroutine, polls until the server is
// listening (or the test times out), and returns a stop function
// that the test should defer to call ShutdownWithContext on the
// shared *fiber.App pointer it gets back.
//
// We can't easily capture the app from inside Run, so we use a
// ConfigureApp callback to stash it for the test.
func runAndWait(t *testing.T, e *Engine[engCtx], opts ...RunOption) (addr string, runErrCh <-chan error, stop func()) {
	t.Helper()
	addr = freeListenAddr(t)
	appCh := make(chan *fiber.App, 1)
	runErr := make(chan error, 1)

	opts = append([]RunOption{
		WithAddr(addr),
		WithConfigureApp(func(app *fiber.App) { appCh <- app }),
	}, opts...)

	go func() { runErr <- e.Run(opts...) }()

	var app *fiber.App
	select {
	case app = <-appCh:
	case <-time.After(2 * time.Second):
		t.Fatal("ConfigureApp was never invoked — Run didn't start")
	}

	// Poll the addr until it accepts a TCP connection.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return addr, runErr, func() { _ = app.Shutdown() }
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server did not start listening on %s within timeout", addr)
	return
}

func TestRun_DefaultsLoadsAndServes(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error {
		return c.SendString("pong")
	})

	addr, runErr, stop := runAndWait(t, e, WithRoutesPath(filepath.Join("testdata", "basic.yaml")))
	defer stop()

	resp, err := http.Get("http://" + addr + "/v1/ping")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "pong" {
		t.Errorf("status=%d body=%q", resp.StatusCode, string(body))
	}

	stop()
	select {
	case err := <-runErr:
		if err != nil {
			t.Errorf("Run returned %v, want nil after Shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Run did not return after Shutdown")
	}
}

func TestRun_WithRoutesFS(t *testing.T) {
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
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong-fs") })

	addr, runErr, stop := runAndWait(t, e, WithRoutesFS(fsys))
	defer stop()

	resp, err := http.Get("http://" + addr + "/v1/ping")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "pong-fs" {
		t.Errorf("status=%d body=%q", resp.StatusCode, string(body))
	}

	stop()
	<-runErr
}

func TestRun_WithUse_RunsBeforeContextBuilder(t *testing.T) {
	var order []string
	var mu sync.Mutex
	record := func(label string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, label)
	}

	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) {
		record("ctx_builder")
		uid, _ := c.Locals("uid").(string)
		return engCtx{UserID: uid}, nil
	})
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error {
		record("handler")
		return c.SendString("ok-" + c.Data.UserID)
	})

	use := func(c *fiber.Ctx) error {
		record("fiber_use")
		c.Locals("uid", "fromUse")
		return c.Next()
	}

	addr, runErr, stop := runAndWait(t, e,
		WithRoutesPath(filepath.Join("testdata", "basic.yaml")),
		WithUse(use),
	)
	defer stop()

	resp, err := http.Get("http://" + addr + "/v1/ping")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok-fromUse" {
		t.Errorf("body = %q, want ok-fromUse — WithUse didn't run before ContextBuilder", string(body))
	}

	mu.Lock()
	want := []string{"fiber_use", "ctx_builder", "handler"}
	got := append([]string(nil), order...)
	mu.Unlock()
	if !equalSlice(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}

	stop()
	<-runErr
}

func TestRun_LoadErrorBubblesUp(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	err := e.Run(WithRoutesPath("does-not-exist.yaml"), WithAddr(freeListenAddr(t)))
	var fe *Error
	if !errors.As(err, &fe) || fe.Code != CodeFileNotFound {
		t.Errorf("err = %v, want CodeFileNotFound", err)
	}
}

func TestRun_MountErrorBubblesUp(t *testing.T) {
	e := newTestEngine()
	// no ContextBuilder, no handler — Mount will refuse.
	err := e.Run(WithRoutesPath(filepath.Join("testdata", "basic.yaml")), WithAddr(freeListenAddr(t)))
	if err == nil {
		t.Fatal("expected mount error")
	}
	if !strings.Contains(err.Error(), "context_builder") && !strings.Contains(err.Error(), "ContextBuilder") {
		t.Errorf("err = %v, want mention of ContextBuilder", err)
	}
}

func TestRun_PreloadedYAML_DoesNotReload(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("preloaded") })

	// Preload from bytes — Run should NOT try to read routes.yaml.
	if err := e.LoadBytes([]byte(`
groups:
  - prefix: /v1
    routes:
      - { method: GET, path: /ping, handler: ping.handle }
`)); err != nil {
		t.Fatal(err)
	}

	addr, runErr, stop := runAndWait(t, e,
		// Wrong path on purpose — should not be touched.
		WithRoutesPath("nonexistent.yaml"),
	)
	defer stop()

	resp, _ := http.Get("http://" + addr + "/v1/ping")
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "preloaded" {
		t.Errorf("body = %q, want preloaded", string(body))
	}
	stop()
	<-runErr
}

func TestRun_FiberConfig_Applied(t *testing.T) {
	hits := atomic.Int32{}
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error {
		return errors.New("boom")
	})

	addr, runErr, stop := runAndWait(t, e,
		WithRoutesPath(filepath.Join("testdata", "basic.yaml")),
		WithFiberConfig(fiber.Config{
			DisableStartupMessage: true,
			ErrorHandler: func(c *fiber.Ctx, err error) error {
				hits.Add(1)
				return c.Status(http.StatusTeapot).SendString("custom-error")
			},
		}),
	)
	defer stop()

	resp, _ := http.Get("http://" + addr + "/v1/ping")
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusTeapot || string(body) != "custom-error" {
		t.Errorf("status=%d body=%q, want 418/custom-error", resp.StatusCode, string(body))
	}
	if hits.Load() != 1 {
		t.Errorf("custom ErrorHandler hits = %d, want 1", hits.Load())
	}
	stop()
	<-runErr
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
