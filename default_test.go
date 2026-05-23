package fibermap

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestDefault_AppliesOpsBundle(t *testing.T) {
	e := Default[engCtx]()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong") })

	addr, runErr, stop := runAndWait(t, e, WithRoutesPath(filepath.Join("testdata", "basic.yaml")))
	defer stop()

	// Primary route works.
	resp, err := newHTTPClient().Get("http://" + addr + "/v1/ping")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("primary status = %d", resp.StatusCode)
	}
	// RequestID middleware ran — response carries the header.
	if got := resp.Header.Get(HeaderRequestID); len(got) != 16 {
		t.Errorf("X-Request-ID = %q, want 16 hex chars", got)
	}

	// Health check exposed.
	resp, err = newHTTPClient().Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Errorf("healthz status=%d body=%q", resp.StatusCode, string(body))
	}

	// Metrics exposed (Prometheus text format).
	resp, err = newHTTPClient().Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "fibermap_http_requests_total") {
		t.Errorf("metrics missing counters: %s", body)
	}

	stop()
	<-runErr
}

func TestDefault_PanicRecovered(t *testing.T) {
	e := Default[engCtx]()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error {
		panic("default-bundle-panic")
	})

	addr, runErr, stop := runAndWait(t, e, WithRoutesPath(filepath.Join("testdata", "basic.yaml")))
	defer stop()

	resp, err := newHTTPClient().Get("http://" + addr + "/v1/ping")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (Recover default)", resp.StatusCode)
	}
	stop()
	<-runErr
}

func TestDefault_OptionsOverrideDefaults(t *testing.T) {
	// User passes WithMetrics("") at Run time to disable the metrics
	// default — last-write-wins on runConfig.metricsPath.
	e := Default[engCtx]()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong") })

	addr, runErr, stop := runAndWait(t, e,
		WithRoutesPath(filepath.Join("testdata", "basic.yaml")),
		WithMetrics(""), // disable
	)
	defer stop()

	resp, err := newHTTPClient().Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusNotFound {
		t.Errorf("status = %d, want 404 (metrics disabled)", resp.StatusCode)
	}
	stop()
	<-runErr
}
