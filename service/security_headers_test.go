package service

import (
	"context"
	"testing"

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
