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
	ready := make(chan struct{})
	runErr := make(chan error, 1)

	// Register an OnListen hook via ConfigureApp. OnListen fires AFTER
	// Mount completes and the listener is bound — gives us a Go-channel
	// happens-before edge so the test can read engine state without a
	// data race.
	opts = append([]RunOption{
		WithAddr(addr),
		WithConfigureApp(func(app *fiber.App) {
			app.Hooks().OnListen(func(fiber.ListenData) error {
				close(ready)
				return nil
			})
			appCh <- app
		}),
	}, opts...)

	go func() { runErr <- e.Run(opts...) }()

	var app *fiber.App
	select {
	case app = <-appCh:
	case <-time.After(2 * time.Second):
		t.Fatal("ConfigureApp was never invoked — Run didn't start")
	}
	select {
	case <-ready:
		return addr, runErr, func() { _ = app.Shutdown() }
	case <-time.After(2 * time.Second):
		t.Fatalf("server did not start listening on %s within timeout", addr)
	}
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

// newHTTPClient returns a tiny http.Client with short timeouts for tests.
// Tests share a package-level helper instead of using http.DefaultClient
// so they aren't subject to global-timeout surprises.
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Second}
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

func TestRun_PortEnv_UsedWhenNoWithAddr(t *testing.T) {
	// Pick a free port and set it as the PORT env var.
	free := freeListenAddr(t)
	port := strings.TrimPrefix(free, "127.0.0.1:")
	t.Setenv("PORT", port)

	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("ok-port") })

	appCh := make(chan *fiber.App, 1)
	runErr := make(chan error, 1)
	go func() {
		runErr <- e.Run(
			WithRoutesPath(filepath.Join("testdata", "basic.yaml")),
			WithConfigureApp(func(app *fiber.App) { appCh <- app }),
		)
	}()
	app := <-appCh
	defer app.Shutdown()

	// Poll the loopback addr where Run should be listening.
	addr := "127.0.0.1:" + port
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	resp, err := http.Get("http://" + addr + "/v1/ping")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok-port" {
		t.Errorf("body = %q, want ok-port (PORT env didn't take effect)", string(body))
	}
	_ = app.Shutdown()
	<-runErr
}

func TestRun_WithAddrBeatsPortEnv(t *testing.T) {
	// PORT points one way, WithAddr points another — WithAddr must win.
	free := freeListenAddr(t)
	t.Setenv("PORT", "9999") // bogus; WithAddr should override and we never bind to 9999

	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("ok") })

	appCh := make(chan *fiber.App, 1)
	runErr := make(chan error, 1)
	go func() {
		runErr <- e.Run(
			WithAddr(free),
			WithRoutesPath(filepath.Join("testdata", "basic.yaml")),
			WithConfigureApp(func(app *fiber.App) { appCh <- app }),
		)
	}()
	app := <-appCh
	defer app.Shutdown()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", free, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	resp, err := http.Get("http://" + free + "/v1/ping")
	if err != nil {
		t.Fatalf("expected WithAddr to win over PORT=9999, but couldn't reach %s: %v", free, err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	_ = app.Shutdown()
	<-runErr
}

func TestRun_WithHealthCheck_ServesOK(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong") })

	addr, runErr, stop := runAndWait(t, e,
		WithRoutesPath("testdata/basic.yaml"),
		WithHealthCheck("/healthz"),
	)
	defer stop()

	resp, err := newHTTPClient().Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Errorf("status=%d body=%q", resp.StatusCode, string(body))
	}
	stop()
	<-runErr
}

func TestRun_WithHealthCheck_BypassesAuthAndCtxBuilder(t *testing.T) {
	// If healthz went through the chain, ContextBuilder would run AND
	// auth (a panic-on-purpose Use handler) would fire. Neither must.
	e := newTestEngine()
	builderCalled := false
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) {
		builderCalled = true
		return engCtx{}, nil
	})
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong") })

	authThatExplodes := func(c *fiber.Ctx) error { panic("auth must not fire for /healthz") }

	addr, runErr, stop := runAndWait(t, e,
		WithRoutesPath("testdata/basic.yaml"),
		WithHealthCheck("/healthz"),
		WithUse(authThatExplodes),
	)
	defer stop()

	resp, err := newHTTPClient().Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if builderCalled {
		t.Error("ContextBuilder ran for /healthz — should be bypassed")
	}
	stop()
	<-runErr
}

func TestRun_WithHealthCheck_DoesNotShowInRoutes(t *testing.T) {
	// Run() registers the health-check handler directly on
	// *fiber.App via app.Get — it never touches the engine's
	// planned-route slice. Verify by combining the same Mount call
	// Run uses with a sibling app.Get on the same app and reading
	// Engine.Routes(). No Listen needed (sidesteps a fiber shutdown
	// quirk that hangs on apps with no served requests).
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong") })
	if err := e.LoadFile(filepath.Join("testdata", "basic.yaml")); err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	app.Get("/healthz", func(c *fiber.Ctx) error { return c.SendString("ok") })
	if err := e.Mount(app); err != nil {
		t.Fatal(err)
	}

	for _, r := range e.Routes() {
		if r.Path == "/healthz" {
			t.Errorf("health-check route leaked into Routes(): %+v", r)
		}
	}
}
