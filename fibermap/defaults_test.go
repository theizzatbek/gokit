package fibermap

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// TestRunDefaults_NewIncludesBundle proves that even
// fibermap.New[T]().Run() — with no opts — installs Recover,
// RequestID, RequestLogger, and HealthCheck.
func TestRunDefaults_NewIncludesBundle(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong") })

	addr, runErr, stop := runAndWait(t, e, WithRoutesPath(filepath.Join("testdata", "basic.yaml")))
	defer stop()

	resp, err := newHTTPClient().Get("http://" + addr + "/v1/ping")
	if err != nil {
		t.Fatal(err)
	}
	// RequestID default produces a 16-hex header on the response.
	if got := resp.Header.Get(HeaderRequestID); len(got) != 16 {
		t.Errorf("X-Request-ID = %q, want 16 hex chars", got)
	}

	// HealthCheck default exposes /healthz.
	resp, err = newHTTPClient().Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Errorf("healthz: status=%d body=%q", resp.StatusCode, string(body))
	}

	// Metrics is NOT a default — opt-in only.
	resp, err = newHTTPClient().Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusNotFound {
		t.Errorf("/metrics status = %d, want 404 (not a default)", resp.StatusCode)
	}
	stop()
	<-runErr
}

func TestRunDefaults_RecoverCatchesPanic(t *testing.T) {
	// New[T]().Run() with no opts must still recover panics.
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error {
		panic("default-recover-test")
	})

	addr, runErr, stop := runAndWait(t, e, WithRoutesPath(filepath.Join("testdata", "basic.yaml")))
	defer stop()

	resp, err := newHTTPClient().Get("http://" + addr + "/v1/ping")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	stop()
	<-runErr
}

func TestRunDefaults_WithoutOptionsDisable(t *testing.T) {
	// Verify each Without* option suppresses its default.
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong") })

	addr, runErr, stop := runAndWait(t, e,
		WithRoutesPath(filepath.Join("testdata", "basic.yaml")),
		WithoutRequestID(),
		WithoutHealthCheck(),
		WithoutRequestLogger(),
		WithoutRecover(),
	)
	defer stop()

	resp, _ := newHTTPClient().Get("http://" + addr + "/v1/ping")
	if got := resp.Header.Get(HeaderRequestID); got != "" {
		t.Errorf("X-Request-ID present despite WithoutRequestID: %q", got)
	}

	resp, _ = newHTTPClient().Get("http://" + addr + "/healthz")
	if resp.StatusCode != fiber.StatusNotFound {
		t.Errorf("/healthz status = %d, want 404 after WithoutHealthCheck", resp.StatusCode)
	}
	stop()
	<-runErr
}

func TestRunDefaults_UserLoggerWinsOverBuiltIn(t *testing.T) {
	// When user passes WithRequestLogger(myLogger), the built-in
	// default's slog.Default() must NOT be used. Captured log output
	// must go to the user's logger.
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong") })

	addr, runErr, stop := runAndWait(t, e,
		WithRoutesPath(filepath.Join("testdata", "basic.yaml")),
		WithRequestLogger(logger),
	)
	defer stop()

	if _, err := newHTTPClient().Get("http://" + addr + "/v1/ping"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"path":"/v1/ping"`) {
		t.Errorf("user logger did not capture request: %s", buf.String())
	}
	stop()
	<-runErr
}

func TestRunDefaults_CustomHealthPathOverrides(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong") })

	addr, runErr, stop := runAndWait(t, e,
		WithRoutesPath(filepath.Join("testdata", "basic.yaml")),
		WithHealthCheck("/_health"),
	)
	defer stop()

	// Default /healthz must NOT be installed when the user moved it.
	resp, _ := newHTTPClient().Get("http://" + addr + "/healthz")
	if resp.StatusCode != fiber.StatusNotFound {
		t.Errorf("/healthz still wired after WithHealthCheck(/_health): %d", resp.StatusCode)
	}
	resp, _ = newHTTPClient().Get("http://" + addr + "/_health")
	if resp.StatusCode != 200 {
		t.Errorf("/_health status = %d, want 200", resp.StatusCode)
	}
	stop()
	<-runErr
}

func TestRunDefaults_DefaultAddsMetricsOver_New(t *testing.T) {
	// fibermap.Default[T] is now just New + Metrics. Prove the
	// metrics endpoint is wired by hitting it and asserting the
	// in-flight gauge series shows up (it's exported even when no
	// labelled counter has been observed yet).
	e := Default[engCtx]()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong") })

	addr, runErr, stop := runAndWait(t, e, WithRoutesPath(filepath.Join("testdata", "basic.yaml")))
	defer stop()

	// Hit a real route first so the counter gets labelled data.
	if _, err := newHTTPClient().Get("http://" + addr + "/v1/ping"); err != nil {
		t.Fatal(err)
	}
	resp, _ := newHTTPClient().Get("http://" + addr + "/metrics")
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `fibermap_http_requests_total{method="GET"`) {
		t.Errorf("Default[T] didn't enable /metrics: %s", body)
	}
	stop()
	<-runErr
}

func TestRunDefaults_WithoutMetricsOverridesDefault(t *testing.T) {
	e := Default[engCtx]()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong") })

	addr, runErr, stop := runAndWait(t, e,
		WithRoutesPath(filepath.Join("testdata", "basic.yaml")),
		WithoutMetrics(),
	)
	defer stop()

	resp, _ := newHTTPClient().Get("http://" + addr + "/metrics")
	if resp.StatusCode != fiber.StatusNotFound {
		t.Errorf("/metrics status = %d, want 404 after WithoutMetrics", resp.StatusCode)
	}
	stop()
	<-runErr
}
