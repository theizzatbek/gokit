package otelkit_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/otelkit"
)

func TestSetupMetrics_RequiresServiceName(t *testing.T) {
	_, err := otelkit.SetupMetrics(context.Background(), "", prometheus.NewRegistry())
	if err == nil {
		t.Fatal("expected error for empty service name, got nil")
	}
	if !strings.Contains(err.Error(), "serviceName") {
		t.Errorf("err = %v, expected mention of serviceName", err)
	}
}

func TestSetupMetrics_RequiresGatherer(t *testing.T) {
	_, err := otelkit.SetupMetrics(context.Background(), "svc", nil)
	if err == nil {
		t.Fatal("expected error for nil gatherer, got nil")
	}
}

func TestSetupMetrics_ReturnsShutdown(t *testing.T) {
	ctx := context.Background()
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_counter", Help: "x"})
	reg.MustRegister(c)
	c.Inc()

	shutdown, err := otelkit.SetupMetrics(ctx, "test-service", reg)
	if err != nil {
		t.Fatalf("SetupMetrics: %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown is nil")
	}
	sCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// Without a reachable collector the final flush returns a
	// transport error — that's the contract surface, not a test
	// failure. We assert the function returns within the deadline.
	_ = shutdown(sCtx)
}

func TestSetupMetrics_ShutdownIsIdempotent(t *testing.T) {
	ctx := context.Background()
	reg := prometheus.NewRegistry()
	shutdown, err := otelkit.SetupMetrics(ctx, "svc", reg,
		otelkit.WithMetricsServiceVersion("1.0.0"),
		otelkit.WithMetricsResourceAttribute("region", "us-east-1"),
		otelkit.WithMetricsInterval(30*time.Second),
	)
	if err != nil {
		t.Fatalf("SetupMetrics: %v", err)
	}
	sCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	first := shutdown(sCtx)
	// sync.Once means the second call MUST return nil — the inner
	// MeterProvider.Shutdown is never re-invoked.
	if second := shutdown(sCtx); second != nil {
		t.Errorf("second shutdown returned %v (first was %v), want nil from sync.Once gate", second, first)
	}
}
