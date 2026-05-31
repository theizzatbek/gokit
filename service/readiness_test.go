package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/fibermap"
)

// fakeChecker is a tiny stub satisfying fibermap.Checker for the
// /readyz wiring assertions below.
type fakeChecker struct {
	name string
	err  error
}

func (f *fakeChecker) Name() string                  { return f.name }
func (f *fakeChecker) Check(_ context.Context) error { return f.err }

func newServiceForReadinessTest(t *testing.T, opts ...Option) *Service[map[string]any, any] {
	t.Helper()
	cfg := Config{}
	cfg.Service.LogLevel = "error"
	svc, err := New[map[string]any, any](context.Background(), cfg, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(svc.Close)
	return svc
}

func TestReadinessCheckers_OnlyWiredSubsystems(t *testing.T) {
	// No DB, NATS, Redis configured → checker list is empty.
	svc := newServiceForReadinessTest(t)
	if got := len(svc.readinessCheckers()); got != 0 {
		t.Errorf("len(checkers) = %d, want 0 (no subsystems wired)", got)
	}
}

func TestReadinessCheckers_AppendsExtra(t *testing.T) {
	extra := &fakeChecker{name: "migrate"}
	svc := newServiceForReadinessTest(t, WithReadinessChecker(extra))
	chk := svc.readinessCheckers()
	if len(chk) != 1 {
		t.Fatalf("len = %d, want 1", len(chk))
	}
	if chk[0].Name() != "migrate" {
		t.Errorf("name = %q, want migrate", chk[0].Name())
	}
}

func TestReadinessCheckers_ExtraOrderedAfterKitChecks(t *testing.T) {
	// Two extras → both surface in the appended order; this also
	// guards against accidental prepend/dedupe.
	a := &fakeChecker{name: "a"}
	b := &fakeChecker{name: "b"}
	svc := newServiceForReadinessTest(t, WithReadinessChecker(a, b))
	chk := svc.readinessCheckers()
	if len(chk) != 2 {
		t.Fatalf("len = %d, want 2", len(chk))
	}
	if chk[0].Name() != "a" || chk[1].Name() != "b" {
		t.Errorf("order = %v / %v, want a / b", chk[0].Name(), chk[1].Name())
	}
}

func TestReadiness_AutoMountedAt_readyz(t *testing.T) {
	// End-to-end: install the same /readyz the service would and
	// confirm 200 with `{"status":"ok"}` when no subsystems are wired.
	svc := newServiceForReadinessTest(t)
	app := fiber.New()
	app.Get("/readyz", fibermap.Readiness(svc.readinessCheckers()))
	resp, err := app.Test(httptest.NewRequest("GET", "/readyz", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	if got := out["status"]; got != "ok" {
		t.Errorf("status field = %v, want ok", got)
	}
}

func TestReadiness_FailingExtraChecker_503(t *testing.T) {
	extra := &fakeChecker{name: "migrate", err: errFakeUnready}
	svc := newServiceForReadinessTest(t, WithReadinessChecker(extra))
	app := fiber.New()
	app.Get("/readyz", fibermap.Readiness(svc.readinessCheckers()))
	resp, err := app.Test(httptest.NewRequest("GET", "/readyz", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	if got := out["status"]; got != "degraded" {
		t.Errorf("status = %v, want degraded", got)
	}
	checks := out["checks"].(map[string]any)
	if got := checks["migrate"]; got != errFakeUnready.Error() {
		t.Errorf("migrate check = %v, want %q", got, errFakeUnready.Error())
	}
}

var errFakeUnready = stringErr("not warm yet")

type stringErr string

func (s stringErr) Error() string { return string(s) }
