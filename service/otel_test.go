package service

import (
	"context"
	"testing"

	"github.com/theizzatbek/gokit/otelkit"
)

func TestSetupOtel_EmptyServiceNameIsNoop(t *testing.T) {
	s := &Service[struct{}, struct{}]{opts: &options{}}
	if err := s.setupOtel(context.Background()); err != nil {
		t.Fatalf("setupOtel: %v", err)
	}
	if s.otelShutdown != nil {
		t.Errorf("otelShutdown should be nil when WithOtel wasn't passed")
	}
	if len(s.opts.fiberMiddleware) != 0 {
		t.Errorf("fiberMiddleware = %d, want 0", len(s.opts.fiberMiddleware))
	}
	if len(s.opts.httpcOpts) != 0 {
		t.Errorf("httpcOpts = %d, want 0", len(s.opts.httpcOpts))
	}
}

func TestSetupOtel_WiresMiddlewareAndHTTPC(t *testing.T) {
	s := &Service[struct{}, struct{}]{
		opts: &options{otelServiceName: "test-svc"},
	}
	if err := s.setupOtel(context.Background()); err != nil {
		t.Fatalf("setupOtel: %v", err)
	}
	if s.otelShutdown == nil {
		t.Error("otelShutdown should be non-nil after Setup")
	}
	if len(s.opts.fiberMiddleware) != 1 {
		t.Errorf("fiberMiddleware = %d, want 1 (otelfiber prepended)", len(s.opts.fiberMiddleware))
	}
	if len(s.opts.httpcOpts) != 1 {
		t.Errorf("httpcOpts = %d, want 1 (otelhttp base transport)", len(s.opts.httpcOpts))
	}
	// Cleanup so the global tracer provider is left in a clean state
	// for subsequent tests in the package.
	if s.otelShutdown != nil {
		_ = s.otelShutdown(context.Background())
	}
}

func TestWithOtel_StoresConfigOnOptions(t *testing.T) {
	o := &options{}
	WithOtel("payments", otelkit.WithServiceVersion("2.1.0"))(o)
	if o.otelServiceName != "payments" {
		t.Errorf("otelServiceName = %q, want payments", o.otelServiceName)
	}
	if len(o.otelOpts) != 1 {
		t.Errorf("otelOpts = %d, want 1", len(o.otelOpts))
	}
}

func TestRegisterOtelShutdown_NoopWhenNil(t *testing.T) {
	s := &Service[struct{}, struct{}]{opts: &options{}}
	s.registerOtelShutdown() // should not panic
	if len(s.shutdownFns) != 0 {
		t.Errorf("shutdownFns = %d, want 0", len(s.shutdownFns))
	}
}

func TestRegisterOtelShutdown_AddsCallback(t *testing.T) {
	called := false
	s := &Service[struct{}, struct{}]{
		opts: &options{},
		otelShutdown: func(ctx context.Context) error {
			called = true
			return nil
		},
	}
	s.registerOtelShutdown()
	if len(s.shutdownFns) != 1 {
		t.Fatalf("shutdownFns = %d, want 1", len(s.shutdownFns))
	}
	s.Close()
	if !called {
		t.Errorf("otelShutdown was not invoked during Close")
	}
}
