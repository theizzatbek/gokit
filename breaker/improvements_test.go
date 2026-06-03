package breaker

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// improvementsClock is a test-only deterministic clock — fakeClock
// from breaker_test.go uses a sync.Mutex, but here we want an atomic
// counter so the helper closures don't need locking.
type improvementsClock struct {
	ns *atomic.Int64
}

func newClock(start time.Time) *improvementsClock {
	x := &atomic.Int64{}
	x.Store(start.UnixNano())
	return &improvementsClock{ns: x}
}

func (c *improvementsClock) Now() time.Time { return time.Unix(0, c.ns.Load()) }
func (c *improvementsClock) Advance(d time.Duration) {
	c.ns.Add(int64(d))
}

func tripFully(t *testing.T, b *Breaker, failures int) {
	t.Helper()
	want := errors.New("boom")
	for i := 0; i < failures; i++ {
		if err := b.Execute(func() error { return want }); err == nil || !errors.Is(err, want) {
			t.Fatalf("execute %d: %v", i, err)
		}
	}
}

// ── A. Adaptive OpenInterval ───────────────────────────────────────────

func TestBreaker_AdaptiveOpenInterval_Exponential(t *testing.T) {
	clk := newClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	b, err := New(Config{
		Name:                   "adapt",
		FailureThreshold:       2,
		MinimumRequests:        2,
		OpenInterval:           10 * time.Second,
		OpenIntervalMultiplier: 3.0,
		OpenIntervalMax:        90 * time.Second,
		WindowDuration:         10 * time.Second,
		WindowSize:             10,
		HalfOpenMaxProbes:      1,
		Now:                    clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}

	// First trip → base interval.
	tripFully(t, b, 2)
	s := b.Stats()
	if s.CurrentOpenInterval != 10*time.Second {
		t.Errorf("first trip interval = %v, want 10s", s.CurrentOpenInterval)
	}

	// Advance past first interval → half-open. Failure → open again
	// (second trip).
	clk.Advance(11 * time.Second)
	if err := b.Execute(func() error { return errors.New("still bad") }); err == nil {
		t.Fatal("probe should fail")
	}
	s = b.Stats()
	if s.CurrentOpenInterval != 30*time.Second {
		t.Errorf("second trip interval = %v, want 30s (10s × 3)", s.CurrentOpenInterval)
	}

	// Third trip → 90s = max.
	clk.Advance(31 * time.Second)
	if err := b.Execute(func() error { return errors.New("still bad") }); err == nil {
		t.Fatal("probe should fail")
	}
	s = b.Stats()
	if s.CurrentOpenInterval != 90*time.Second {
		t.Errorf("third trip interval = %v, want 90s (capped at max)", s.CurrentOpenInterval)
	}
}

func TestBreaker_AdaptiveOpenInterval_ResetsOnClose(t *testing.T) {
	clk := newClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	b, _ := New(Config{
		Name:                   "reset",
		FailureThreshold:       1,
		MinimumRequests:        1,
		OpenInterval:           10 * time.Second,
		OpenIntervalMultiplier: 3.0,
		HalfOpenMaxProbes:      1,
		Now:                    clk.Now,
	})
	tripFully(t, b, 1)
	if b.Stats().ConsecutiveTrips != 1 {
		t.Fatalf("trips = %d, want 1", b.Stats().ConsecutiveTrips)
	}

	clk.Advance(11 * time.Second)
	// Successful probe → close.
	if err := b.Execute(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := b.Stats().ConsecutiveTrips; got != 0 {
		t.Errorf("trips after close = %d, want 0 (reset)", got)
	}
}

// ── B. OnStateChange hook ─────────────────────────────────────────────

func TestBreaker_OnStateChangeFires(t *testing.T) {
	var transitions atomic.Int32
	var saw struct {
		closedToOpen bool
		openToHalf   bool
	}
	clk := newClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	b, _ := New(Config{
		Name:              "hook",
		FailureThreshold:  1,
		MinimumRequests:   1,
		OpenInterval:      10 * time.Second,
		HalfOpenMaxProbes: 1,
		Now:               clk.Now,
		OnStateChange: func(from, to State) {
			transitions.Add(1)
			if from == StateClosed && to == StateOpen {
				saw.closedToOpen = true
			}
			if from == StateOpen && to == StateHalfOpen {
				saw.openToHalf = true
			}
		},
	})
	tripFully(t, b, 1)
	clk.Advance(11 * time.Second)
	_ = b.Execute(func() error { return nil })

	if !saw.closedToOpen {
		t.Error("closed → open transition not observed")
	}
	if !saw.openToHalf {
		t.Error("open → half_open transition not observed")
	}
	if transitions.Load() < 2 {
		t.Errorf("transitions = %d, want >= 2", transitions.Load())
	}
}

func TestBreaker_OnStateChangePanicSafe(t *testing.T) {
	clk := newClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	b, _ := New(Config{
		Name:              "panic-hook",
		FailureThreshold:  1,
		MinimumRequests:   1,
		HalfOpenMaxProbes: 1,
		Now:               clk.Now,
		OnStateChange:     func(from, to State) { panic("user error") },
	})
	// Must not propagate the panic outside the breaker.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic escaped breaker: %v", r)
		}
	}()
	tripFully(t, b, 1)
}

// ── C. ForceOpen / ForceClose ─────────────────────────────────────────

func TestBreaker_ForceOpen_HoldsForDuration(t *testing.T) {
	clk := newClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	b, _ := New(Config{
		Name:              "force",
		OpenInterval:      time.Second, // would normally allow probe quickly
		HalfOpenMaxProbes: 1,
		Now:               clk.Now,
	})
	b.ForceOpen(time.Hour)
	if s := b.Stats().State; s != StateOpen {
		t.Errorf("state = %v, want open", s)
	}
	// Even after natural OpenInterval, breaker stays open under
	// the forced window.
	clk.Advance(2 * time.Second)
	if allowed, _ := b.Allow(); allowed {
		t.Error("Allow should still short-circuit under forced window")
	}
}

func TestBreaker_ForceClose_ClearsState(t *testing.T) {
	clk := newClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	b, _ := New(Config{
		Name:              "fc",
		FailureThreshold:  1,
		MinimumRequests:   1,
		HalfOpenMaxProbes: 1,
		Now:               clk.Now,
	})
	tripFully(t, b, 1)
	if b.Stats().State != StateOpen {
		t.Fatal("setup: must be open after trip")
	}

	b.ForceClose()
	s := b.Stats()
	if s.State != StateClosed {
		t.Errorf("state = %v, want closed", s.State)
	}
	if s.ConsecutiveTrips != 0 {
		t.Errorf("trips = %d, want 0 after ForceClose", s.ConsecutiveTrips)
	}
	if allowed, _ := b.Allow(); !allowed {
		t.Error("Allow should permit after ForceClose")
	}
}

// ── D. Stats() snapshot ───────────────────────────────────────────────

func TestBreaker_Stats_ReportsCounters(t *testing.T) {
	clk := newClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	b, _ := New(Config{
		Name:             "stats",
		FailureThreshold: 5,
		MinimumRequests:  5,
		HalfOpenMaxProbes: 1,
		Now:              clk.Now,
	})
	// Two successes, one failure — not enough to trip.
	_ = b.Execute(func() error { return nil })
	_ = b.Execute(func() error { return nil })
	_ = b.Execute(func() error { return errors.New("e") })

	s := b.Stats()
	if s.State != StateClosed {
		t.Errorf("state = %v, want closed (under threshold)", s.State)
	}
	if s.WindowRequests != 3 {
		t.Errorf("window requests = %d, want 3", s.WindowRequests)
	}
	if s.WindowFailures != 1 {
		t.Errorf("window failures = %d, want 1", s.WindowFailures)
	}
}

func TestBreaker_StatsNilReceiverSafe(t *testing.T) {
	var b *Breaker
	if got := b.Stats(); got.State != StateClosed {
		t.Errorf("nil Stats.State = %v, want closed (zero)", got.State)
	}
}

// ── E. HalfOpenSuccessThreshold ───────────────────────────────────────

func TestBreaker_KofN_SuccessThreshold(t *testing.T) {
	clk := newClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	b, err := New(Config{
		Name:                     "kofn",
		FailureThreshold:         1,
		MinimumRequests:          1,
		OpenInterval:             time.Second,
		HalfOpenMaxProbes:        5,
		HalfOpenSuccessThreshold: 3, // 3 of 5 must succeed
		Now:                      clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}

	tripFully(t, b, 1)
	clk.Advance(2 * time.Second) // enter half-open on next Allow

	// 3 successful probes → close.
	for i := 0; i < 3; i++ {
		allowed, done := b.Allow()
		if !allowed {
			t.Fatalf("probe %d not allowed", i)
		}
		done(true)
	}
	if s := b.Stats().State; s != StateClosed {
		t.Errorf("state = %v, want closed after 3 successes (K=3)", s)
	}
}

func TestBreaker_KofN_ThresholdValidation(t *testing.T) {
	_, err := New(Config{
		Name:                     "bad",
		HalfOpenMaxProbes:        3,
		HalfOpenSuccessThreshold: 5, // K > N must fail
	})
	if err == nil {
		t.Error("expected validation error for K > N")
	}
}
