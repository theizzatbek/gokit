package breaker

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetrics_StateGaugeFollowsTransitions(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newTestBreaker(t, clk, Config{
		FailureThreshold:  2,
		MinimumRequests:   2,
		WindowDuration:    10 * time.Second,
		WindowSize:        10,
		OpenInterval:      30 * time.Second,
		HalfOpenMaxProbes: 1,
		Metrics:           reg,
	})

	if got := testutil.ToFloat64(b.collector.state); got != 0 {
		t.Errorf("initial gauge = %v, want 0 (closed)", got)
	}

	// Trip the breaker.
	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errors.New("x") })
	}
	if got := testutil.ToFloat64(b.collector.state); got != 1 {
		t.Errorf("after trip: gauge = %v, want 1 (open)", got)
	}

	// Move to half-open via probe.
	clk.Advance(31 * time.Second)
	allowed, done := b.Allow()
	if !allowed {
		t.Fatal("probe denied")
	}
	if got := testutil.ToFloat64(b.collector.state); got != 2 {
		t.Errorf("after probe entry: gauge = %v, want 2 (half_open)", got)
	}
	done(true)
	if got := testutil.ToFloat64(b.collector.state); got != 0 {
		t.Errorf("after recovery: gauge = %v, want 0 (closed)", got)
	}
}

func TestMetrics_ShortCircuitCounter(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newTestBreaker(t, clk, Config{
		FailureThreshold:  2,
		MinimumRequests:   2,
		WindowDuration:    10 * time.Second,
		WindowSize:        10,
		OpenInterval:      30 * time.Second,
		HalfOpenMaxProbes: 1,
		Metrics:           reg,
	})
	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errors.New("x") })
	}
	for i := 0; i < 5; i++ {
		_ = b.Execute(func() error { return nil })
	}
	if got := testutil.ToFloat64(b.collector.shortCircuits); got != 5 {
		t.Errorf("short_circuits = %v, want 5", got)
	}
}

func TestMetrics_TransitionsCounter(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newTestBreaker(t, clk, Config{
		FailureThreshold:  2,
		MinimumRequests:   2,
		WindowDuration:    10 * time.Second,
		WindowSize:        10,
		OpenInterval:      30 * time.Second,
		HalfOpenMaxProbes: 1,
		Metrics:           reg,
	})
	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errors.New("x") })
	}
	clk.Advance(31 * time.Second)
	_ = b.Execute(func() error { return nil })

	closedToOpen := testutil.ToFloat64(b.collector.transitions.WithLabelValues("closed", "open"))
	openToHalfOpen := testutil.ToFloat64(b.collector.transitions.WithLabelValues("open", "half_open"))
	halfOpenToClosed := testutil.ToFloat64(b.collector.transitions.WithLabelValues("half_open", "closed"))
	if closedToOpen != 1 || openToHalfOpen != 1 || halfOpenToClosed != 1 {
		t.Errorf("transitions = closed→open:%v, open→half_open:%v, half_open→closed:%v, want 1/1/1",
			closedToOpen, openToHalfOpen, halfOpenToClosed)
	}
}

func TestMetrics_NameLabelIsConstant(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	_ = newTestBreaker(t, clk, Config{
		Name:    "stripe",
		Metrics: reg,
	})

	got, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	found := false
	for _, mf := range got {
		if mf.GetName() != "breaker_state" {
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
		t.Errorf("breaker_state{name=stripe} not exported; got %d families", len(got))
	}
}

func TestMetrics_TwoBreakersCoexistOnSameRegistry(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	if _, err := New(Config{Name: "stripe", Metrics: reg, Now: clk.Now}); err != nil {
		t.Fatalf("first breaker: %v", err)
	}
	// Same registry, different name = no collision.
	if _, err := New(Config{Name: "twilio", Metrics: reg, Now: clk.Now}); err != nil {
		t.Fatalf("second breaker: %v", err)
	}
}

func TestMetrics_DuplicateNameOnSameRegistryPanics(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	if _, err := New(Config{Name: "dup", Metrics: reg, Now: clk.Now}); err != nil {
		t.Fatalf("first: %v", err)
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate name + same registry")
		}
		// Sanity: the panic is from prometheus's collector registration.
		if msg, ok := r.(error); ok {
			if !strings.Contains(msg.Error(), "duplicate") && !strings.Contains(msg.Error(), "already registered") {
				t.Logf("panic message: %v", msg)
			}
		}
	}()
	_, _ = New(Config{Name: "dup", Metrics: reg, Now: clk.Now})
}
