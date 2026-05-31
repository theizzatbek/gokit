package fibermap

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// fakeChecker is the minimal Checker for test cases.
type fakeChecker struct {
	name string
	err  error
	wait time.Duration
}

func (f *fakeChecker) Name() string { return f.name }
func (f *fakeChecker) Check(ctx context.Context) error {
	if f.wait > 0 {
		select {
		case <-time.After(f.wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

func sendReadiness(t *testing.T, h fiber.Handler) (int, map[string]any) {
	t.Helper()
	app := fiber.New()
	app.Get("/readyz", h)
	resp, err := app.Test(httptest.NewRequest("GET", "/readyz", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	return resp.StatusCode, out
}

func TestReadiness_AllPassReturns200(t *testing.T) {
	h := Readiness([]Checker{
		&fakeChecker{name: "db"},
		&fakeChecker{name: "nats"},
	})
	status, body := sendReadiness(t, h)
	if status != fiber.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if got := body["status"]; got != "ok" {
		t.Errorf("status field = %v, want ok", got)
	}
	if _, has := body["checks"]; has {
		t.Errorf("body should not include checks on success: %v", body)
	}
}

func TestReadiness_AnyFailReturns503(t *testing.T) {
	h := Readiness([]Checker{
		&fakeChecker{name: "db"},
		&fakeChecker{name: "nats", err: errors.New("pool exhausted")},
		&fakeChecker{name: "redis"},
	})
	status, body := sendReadiness(t, h)
	if status != fiber.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
	if got := body["status"]; got != "degraded" {
		t.Errorf("status field = %v, want degraded", got)
	}
	checks, ok := body["checks"].(map[string]any)
	if !ok {
		t.Fatalf("checks field = %v, want map", body["checks"])
	}
	if got := checks["nats"]; got != "pool exhausted" {
		t.Errorf("nats check = %v, want 'pool exhausted'", got)
	}
	if _, has := checks["db"]; has {
		t.Errorf("successful checks should not appear in body: %v", checks)
	}
}

func TestReadiness_TimeoutSurfacesAsCtxError(t *testing.T) {
	h := Readiness([]Checker{
		&fakeChecker{name: "slow", wait: time.Second},
	}, WithReadinessTimeout(20*time.Millisecond))
	status, body := sendReadiness(t, h)
	if status != fiber.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", status)
	}
	checks := body["checks"].(map[string]any)
	if got, _ := checks["slow"].(string); got == "" {
		t.Errorf("slow check should carry a timeout error; got %v", checks)
	}
}

func TestReadiness_NoCheckers_AllOk(t *testing.T) {
	h := Readiness(nil)
	status, body := sendReadiness(t, h)
	if status != fiber.StatusOK {
		t.Errorf("status = %d, want 200 (no checkers ⇒ trivially ok)", status)
	}
	if got := body["status"]; got != "ok" {
		t.Errorf("status field = %v, want ok", got)
	}
}

func TestReadiness_ParallelDispatch(t *testing.T) {
	// Two 100ms checks running in parallel should finish in ~100ms,
	// not 200ms. Verifies the WaitGroup goroutines fan out instead
	// of running sequentially.
	h := Readiness([]Checker{
		&fakeChecker{name: "a", wait: 100 * time.Millisecond},
		&fakeChecker{name: "b", wait: 100 * time.Millisecond},
	}, WithReadinessTimeout(time.Second))

	app := fiber.New()
	app.Get("/readyz", h)

	start := time.Now()
	resp, err := app.Test(httptest.NewRequest("GET", "/readyz", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if elapsed := time.Since(start); elapsed > 180*time.Millisecond {
		t.Errorf("readiness took %v, want < 180ms (parallel dispatch broken)", elapsed)
	}
}
