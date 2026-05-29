package otelkit_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/otelkit"
)

func TestSetup_RequiresServiceName(t *testing.T) {
	_, err := otelkit.Setup(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty service name, got nil")
	}
	if !strings.Contains(err.Error(), "serviceName") {
		t.Errorf("err = %v, expected mention of serviceName", err)
	}
}

func TestSetup_ReturnsShutdown(t *testing.T) {
	ctx := context.Background()
	shutdown, err := otelkit.Setup(ctx, "test-service")
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown is nil")
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := shutdown(shutdownCtx); err != nil {
		t.Errorf("shutdown returned %v", err)
	}
}

func TestSetup_ShutdownIsIdempotent(t *testing.T) {
	ctx := context.Background()
	shutdown, err := otelkit.Setup(ctx, "test-service",
		otelkit.WithServiceVersion("1.0.0"),
		otelkit.WithResourceAttribute("region", "us-east-1"))
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	sCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := shutdown(sCtx); err != nil {
		t.Errorf("first shutdown: %v", err)
	}
	// Second call should be a no-op via sync.Once.
	if err := shutdown(sCtx); err != nil {
		t.Errorf("second shutdown: %v", err)
	}
}

func TestSetup_AcceptsAllOptions(t *testing.T) {
	// Smoke test — every Option compiles + applies cleanly.
	ctx := context.Background()
	shutdown, err := otelkit.Setup(ctx, "svc",
		otelkit.WithServiceVersion("0.1.0"),
		otelkit.WithSampleRatio(0.5),
		otelkit.WithResourceAttribute("deployment.environment", "test"),
	)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	sCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = shutdown(sCtx)
}
