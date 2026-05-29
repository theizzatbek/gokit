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

	mc.setPoolStat("primary", poolStat{Acquired: 3, Idle: 7, Max: 10, Total: 10})

	if got := testutil.ToFloat64(mc.poolSize.WithLabelValues("primary", "acquired")); got != 3 {
		t.Fatalf("acquired = %v, want 3", got)
	}
	if got := testutil.ToFloat64(mc.poolSize.WithLabelValues("primary", "idle")); got != 7 {
		t.Fatalf("idle = %v, want 7", got)
	}
	if got := testutil.ToFloat64(mc.poolSize.WithLabelValues("primary", "max")); got != 10 {
		t.Fatalf("max = %v, want 10", got)
	}
}

func TestMetricsCollector_PoolGaugesIndependentLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	mc := newMetricsCollector(reg)

	mc.setPoolStat("primary", poolStat{Acquired: 3, Idle: 7, Max: 10, Total: 10})
	mc.setPoolStat("standby", poolStat{Acquired: 1, Idle: 4, Max: 5, Total: 5})

	if got := testutil.ToFloat64(mc.poolSize.WithLabelValues("primary", "acquired")); got != 3 {
		t.Fatalf("primary acquired = %v, want 3", got)
	}
	if got := testutil.ToFloat64(mc.poolSize.WithLabelValues("standby", "acquired")); got != 1 {
		t.Fatalf("standby acquired = %v, want 1", got)
	}
	if got := testutil.ToFloat64(mc.poolSize.WithLabelValues("standby", "max")); got != 5 {
		t.Fatalf("standby max = %v, want 5", got)
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

func TestTracer_SlowQueryIncrementsCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	mc := newMetricsCollector(reg)
	tr := &tracer{metrics: mc, slowThreshold: 1 * time.Nanosecond}

	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	// Force "elapsed > threshold" — sleep one tick.
	time.Sleep(10 * time.Microsecond)
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})

	if got := testutil.ToFloat64(mc.slowQuery); got != 1 {
		t.Errorf("db_slow_query_total = %v, want 1", got)
	}
}

func TestTracer_ErroredQueryDoesNotCountAsSlow(t *testing.T) {
	// Errored queries already feed db_query_duration_seconds{outcome=error}
	// — they must NOT also count as slow, otherwise a slow-DB outage
	// double-counts on both panels.
	reg := prometheus.NewRegistry()
	mc := newMetricsCollector(reg)
	tr := &tracer{metrics: mc, slowThreshold: 1 * time.Nanosecond}

	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "SELECT 1"})
	time.Sleep(10 * time.Microsecond)
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("boom")})

	if got := testutil.ToFloat64(mc.slowQuery); got != 0 {
		t.Errorf("errored query counted as slow: db_slow_query_total = %v, want 0", got)
	}
}

func TestMetricsCollector_ObserveTx_Variants(t *testing.T) {
	reg := prometheus.NewRegistry()
	mc := newMetricsCollector(reg)

	mc.observeTx("tx", "commit", 5*time.Millisecond)
	mc.observeTx("tx", "commit", 8*time.Millisecond)
	mc.observeTx("tx", "rollback", 12*time.Millisecond)
	mc.observeTx("savepoint", "commit", 2*time.Millisecond)
	mc.observeTx("savepoint", "panic", 1*time.Millisecond)

	if got := testutil.ToFloat64(mc.txTotal.WithLabelValues("tx", "commit")); got != 2 {
		t.Errorf("tx commit count = %v, want 2", got)
	}
	if got := testutil.ToFloat64(mc.txTotal.WithLabelValues("tx", "rollback")); got != 1 {
		t.Errorf("tx rollback count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(mc.txTotal.WithLabelValues("savepoint", "commit")); got != 1 {
		t.Errorf("savepoint commit count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(mc.txTotal.WithLabelValues("savepoint", "panic")); got != 1 {
		t.Errorf("savepoint panic count = %v, want 1", got)
	}
}

func TestMetricsCollector_ObserveTx_NilSafe(t *testing.T) {
	var mc *metricsCollector
	// Should not panic. db.DB.Tx is called without WithMetrics in many
	// callers — observeTx must be a no-op then.
	mc.observeTx("tx", "commit", time.Millisecond)
	mc.incSlowQuery()
}
