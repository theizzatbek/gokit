package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/fibermap"
)

func newServiceForSecHeadersTest(t *testing.T, opts ...Option) *Service[map[string]any, any] {
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

// We can't run the full app stack inside a unit test (Run blocks
// on Listen), but we CAN verify that the options struct ends up in
// the right state to drive runOptions(). The runOptions() output is
// stable: WithoutSecurityHeaders flips skipSecurityHeaders;
// WithSecurityHeaders accumulates into securityHeaderOpts.

func TestSecurityHeadersDefault_NotSkipped(t *testing.T) {
	svc := newServiceForSecHeadersTest(t)
	if svc.opts.skipSecurityHeaders {
		t.Error("default should NOT skip security headers")
	}
}

func TestSecurityHeaders_WithoutSecurityHeadersSetsSkip(t *testing.T) {
	svc := newServiceForSecHeadersTest(t, WithoutSecurityHeaders())
	if !svc.opts.skipSecurityHeaders {
		t.Error("WithoutSecurityHeaders should flip skipSecurityHeaders")
	}
}

func TestSecurityHeaders_WithSecurityHeadersAccumulates(t *testing.T) {
	svc := newServiceForSecHeadersTest(t,
		WithSecurityHeaders(fibermap.WithHSTSIncludeSubdomains()),
		WithSecurityHeaders(fibermap.WithoutCSP()),
	)
	if got := len(svc.opts.securityHeaderOpts); got != 2 {
		t.Errorf("len(securityHeaderOpts) = %d, want 2", got)
	}
}

func TestBodyLimit_DefaultZero(t *testing.T) {
	svc := newServiceForSecHeadersTest(t)
	if svc.opts.bodyLimit != 0 {
		t.Errorf("bodyLimit default = %d, want 0 (fiber default)", svc.opts.bodyLimit)
	}
}

func TestBodyLimit_Set(t *testing.T) {
	svc := newServiceForSecHeadersTest(t, WithBodyLimit(64*1024))
	if svc.opts.bodyLimit != 64*1024 {
		t.Errorf("bodyLimit = %d, want 65536", svc.opts.bodyLimit)
	}
}

// Regression guards for v1.0.1 P0-3 (LicenseKit followup): the
// fibermap.ErrorHandler must be installed unconditionally so
// kit-typed *errs.Error returns surface as {code, message,
// details[]} JSON even when WithBodyLimit is not set. Pre-fix the
// ErrorHandler install was gated by `bodyLimit > 0`, so a
// service.New caller skipping WithBodyLimit got fiber's default
// 500 plaintext on every typed error.

func TestBuildFiberConfig_AlwaysInstallsErrorHandler_NoBodyLimit(t *testing.T) {
	svc := newServiceForSecHeadersTest(t)
	cfg := svc.buildFiberConfig()
	if cfg.ErrorHandler == nil {
		t.Fatal("ErrorHandler must be installed even without WithBodyLimit")
	}
	if cfg.BodyLimit != 0 {
		t.Errorf("BodyLimit = %d, want 0 (fiber default)", cfg.BodyLimit)
	}
}

func TestBuildFiberConfig_BodyLimitSet_KeepsErrorHandler(t *testing.T) {
	svc := newServiceForSecHeadersTest(t, WithBodyLimit(64*1024))
	cfg := svc.buildFiberConfig()
	if cfg.ErrorHandler == nil {
		t.Fatal("ErrorHandler must be installed when WithBodyLimit is set")
	}
	if cfg.BodyLimit != 64*1024 {
		t.Errorf("BodyLimit = %d, want 65536", cfg.BodyLimit)
	}
}

// Regression guards for v1.1.0 P2-16 (LicenseKit followup):
// service.WithErrorHandler must override the kit default
// fibermap.ErrorHandler. The typical use case is the sentrykit wrap
// shape:
//
//	service.WithErrorHandler(sentrykit.WrapErrorHandler(fibermap.ErrorHandler(logger)))

func TestBuildFiberConfig_WithErrorHandler_OverridesDefault(t *testing.T) {
	sentinel := func(c *fiber.Ctx, err error) error { return nil }
	svc := newServiceForSecHeadersTest(t, WithErrorHandler(sentinel))
	cfg := svc.buildFiberConfig()

	if cfg.ErrorHandler == nil {
		t.Fatal("ErrorHandler is nil after WithErrorHandler — kit default leaked")
	}
	// Compare function pointers via reflect.Value.Pointer.
	got := reflect.ValueOf(cfg.ErrorHandler).Pointer()
	want := reflect.ValueOf(fiber.ErrorHandler(sentinel)).Pointer()
	if got != want {
		t.Errorf("ErrorHandler pointer %x != supplied sentinel %x", got, want)
	}
}

func TestBuildFiberConfig_WithErrorHandlerNil_KeepsKitDefault(t *testing.T) {
	// WithErrorHandler(nil) is documented as "equivalent to never
	// calling WithErrorHandler at all" — the kit default
	// (fibermap.ErrorHandler) must still install.
	svc := newServiceForSecHeadersTest(t, WithErrorHandler(nil))
	cfg := svc.buildFiberConfig()
	if cfg.ErrorHandler == nil {
		t.Fatal("ErrorHandler is nil after WithErrorHandler(nil) — kit default did not fall back")
	}
}

func TestBuildFiberConfig_WithErrorHandler_KeepsBodyLimit(t *testing.T) {
	// WithErrorHandler must compose cleanly with WithBodyLimit —
	// neither option should clobber the other.
	sentinel := func(c *fiber.Ctx, err error) error { return nil }
	svc := newServiceForSecHeadersTest(t,
		WithErrorHandler(sentinel),
		WithBodyLimit(128*1024),
	)
	cfg := svc.buildFiberConfig()

	if cfg.ErrorHandler == nil {
		t.Error("ErrorHandler is nil")
	}
	if cfg.BodyLimit != 128*1024 {
		t.Errorf("BodyLimit = %d, want %d", cfg.BodyLimit, 128*1024)
	}
}
