package bulkhead

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetrics_OutcomeCounters(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	b, err := New(Config{
		Name:          "test",
		MaxConcurrent: 1,
		MaxQueue:      1,
		QueueTimeout:  20 * time.Millisecond,
		Metrics:       reg,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1× ok (fast path)
	r, _ := b.Acquire(context.Background())

	// 1× queue_timeout
	if _, err := b.Acquire(context.Background()); err == nil {
		t.Fatal("expected queue timeout")
	}

	// 1× full (waiter cap exceeded — we already exhausted via a queued
	// timeout; need a fresh waiter exceeding MaxQueue).
	// Re-prime: occupy in-flight + 1 queued waiter, then 3rd should be full.
	// First release so we start fresh.
	r()

	r1, _ := b.Acquire(context.Background())
	go func() {
		r2, _ := b.Acquire(context.Background())
		if r2 != nil {
			r2()
		}
	}()
	// Spin until the waiter is parked.
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if b.Stats().Waiting == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := b.Acquire(context.Background()); err != ErrBulkheadFull {
		t.Fatalf("want full, got %v", err)
	}
	r1() // unblock the waiter goroutine

	okCount := testutil.ToFloat64(b.collector.acquires.WithLabelValues(outcomeOK))
	timeoutCount := testutil.ToFloat64(b.collector.acquires.WithLabelValues(outcomeQueueTimeout))
	fullCount := testutil.ToFloat64(b.collector.acquires.WithLabelValues(outcomeFull))

	if okCount < 2 { // first ok + queued waiter's ok
		t.Errorf("ok count = %v, want >= 2", okCount)
	}
	if timeoutCount != 1 {
		t.Errorf("queue_timeout count = %v, want 1", timeoutCount)
	}
	if fullCount != 1 {
		t.Errorf("full count = %v, want 1", fullCount)
	}
}

func TestMetrics_GaugesReflectState(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	b, err := New(Config{
		Name:          "x",
		MaxConcurrent: 3,
		MaxQueue:      10,
		Metrics:       reg,
	})
	if err != nil {
		t.Fatal(err)
	}

	r1, _ := b.Acquire(context.Background())
	r2, _ := b.Acquire(context.Background())
	defer r1()
	defer r2()

	// inFlight gauge is a GaugeFunc → snapshot via Gather.
	got, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var inFlightSeen float64
	for _, mf := range got {
		if mf.GetName() != "bulkhead_in_flight" {
			continue
		}
		for _, m := range mf.Metric {
			inFlightSeen = m.GetGauge().GetValue()
		}
	}
	if inFlightSeen != 2 {
		t.Errorf("bulkhead_in_flight = %v, want 2", inFlightSeen)
	}
}

func TestMetrics_NameLabelIsConstant(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	_, err := New(Config{Name: "stripe", MaxConcurrent: 1, Metrics: reg})
	if err != nil {
		t.Fatal(err)
	}

	got, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, mf := range got {
		if mf.GetName() != "bulkhead_in_flight" {
			continue
		}
		for _, m := range mf.Metric {
			for _, lp := range m.Label {
				if lp.GetName() == "name" && lp.GetValue() == "stripe" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("bulkhead_in_flight{name=stripe} missing; got %d families", len(got))
	}
}

func TestMetrics_TwoBulkheadsCoexistOnSameRegistry(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	if _, err := New(Config{Name: "stripe", MaxConcurrent: 1, Metrics: reg}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := New(Config{Name: "twilio", MaxConcurrent: 1, Metrics: reg}); err != nil {
		t.Fatalf("second: %v", err)
	}
}
