package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetricsCollector_ObservesSuccessAndError(t *testing.T) {
	reg := prometheus.NewRegistry()
	mc := newMetricsCollector(reg)

	mc.observe(50*time.Millisecond, nil)
	mc.observe(10*time.Millisecond, errors.New("x"))

	if got := testutil.CollectAndCount(mc.duration); got == 0 {
		t.Fatal("duration histogram saw no observations")
	}
}

func TestMetricsCollector_PoolGaugesPopulated(t *testing.T) {
	reg := prometheus.NewRegistry()
	mc := newMetricsCollector(reg)

	mc.setPoolStat(poolStat{Acquired: 3, Idle: 7, Max: 10, Total: 10})

	if got := testutil.ToFloat64(mc.poolSize.WithLabelValues("acquired")); got != 3 {
		t.Fatalf("acquired = %v, want 3", got)
	}
	if got := testutil.ToFloat64(mc.poolSize.WithLabelValues("idle")); got != 7 {
		t.Fatalf("idle = %v, want 7", got)
	}
	if got := testutil.ToFloat64(mc.poolSize.WithLabelValues("max")); got != 10 {
		t.Fatalf("max = %v, want 10", got)
	}
}

func TestTracer_FeedsMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	mc := newMetricsCollector(reg)
	tr := &tracer{metrics: mc, slowThreshold: 0}

	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	if got := testutil.CollectAndCount(mc.duration); got == 0 {
		t.Fatal("tracer did not observe into metrics")
	}
}
