package fibermap

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestMetrics_CountsAndExposes(t *testing.T) {
	mw, reg := Metrics()

	app := fiber.New()
	app.Use(mw)
	app.Get("/users/:id", func(c *fiber.Ctx) error { return c.SendString("user " + c.Params("id")) })
	app.Get("/metrics", MetricsHandler(reg))

	// Three requests against the same route template. Cardinality
	// bounded by route template, not concrete path: even with three
	// different :id values the counter label stays "/users/:id".
	for i, id := range []string{"42", "7", "99"} {
		_ = i
		if _, err := app.Test(httptest.NewRequest("GET", "/users/"+id, nil)); err != nil {
			t.Fatal(err)
		}
	}

	resp, err := app.Test(httptest.NewRequest("GET", "/metrics", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	text := string(body)

	// Counter for the three /users/:id 200 responses — proves the
	// label uses the route template, not the concrete path.
	if !strings.Contains(text, `fibermap_http_requests_total{method="GET",route="/users/:id",status="200"} 3`) {
		t.Errorf("expected counter for /users/:id 200 = 3, got:\n%s", text)
	}
	// Histogram presence (the duration_seconds_count line).
	if !strings.Contains(text, `fibermap_http_request_duration_seconds_count{method="GET",route="/users/:id",status="200"} 3`) {
		t.Errorf("expected histogram count = 3, got:\n%s", text)
	}
	// In-flight gauge present (value is non-deterministic since the
	// metrics request itself counts).
	if !strings.Contains(text, "# TYPE fibermap_http_requests_in_flight gauge") {
		t.Errorf("expected in-flight gauge to be exported, got:\n%s", text)
	}
}

func TestRun_WithMetrics_ExposesEndpoint(t *testing.T) {
	e := newTestEngine()
	e.SetContextBuilder(func(c *fiber.Ctx) (engCtx, error) { return engCtx{}, nil })
	e.RegisterHandler("ping.handle", func(c *Context[engCtx]) error { return c.SendString("pong") })

	addr, runErr, stop := runAndWait(t, e,
		WithRoutesPath("testdata/basic.yaml"),
		WithMetrics("/metrics"),
	)
	defer stop()

	// Hit the real route a few times so the counter goes up.
	for i := 0; i < 2; i++ {
		if _, err := newHTTPClient().Get("http://" + addr + "/v1/ping"); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := newHTTPClient().Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "fibermap_http_requests_total") {
		t.Errorf("metrics endpoint missing counters:\n%s", string(body))
	}
	stop()
	<-runErr
}
