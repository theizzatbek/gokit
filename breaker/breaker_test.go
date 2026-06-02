package breaker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a manual time source for deterministic state-machine
// tests. All access is via methods to keep the underlying field
// concurrency-friendly when the breaker reads from multiple
// goroutines.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestBreaker(t *testing.T, clk *fakeClock, cfg Config) *Breaker {
	t.Helper()
	if cfg.Name == "" {
		cfg.Name = "test"
	}
	cfg.Now = clk.Now
	b, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func TestBreaker_NilReceiverIsNoop(t *testing.T) {
	t.Parallel()
	var b *Breaker
	if got := b.State(); got != StateClosed {
		t.Errorf("nil State() = %v, want %v", got, StateClosed)
	}
	allowed, done := b.Allow()
	if !allowed {
		t.Error("nil Allow() must permit")
	}
	done(false) // must not panic

	if err := b.Execute(func() error { return nil }); err != nil {
		t.Errorf("nil Execute(nil-err) = %v", err)
	}
	want := errors.New("boom")
	if err := b.Execute(func() error { return want }); !errors.Is(err, want) {
		t.Errorf("nil Execute(err) propagation broken: %v", err)
	}
}

func TestBreaker_StartsClosed(t *testing.T) {
	t.Parallel()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newTestBreaker(t, clk, Config{})
	if got := b.State(); got != StateClosed {
		t.Errorf("State = %v, want closed", got)
	}
}

func TestBreaker_TripsOnThreshold(t *testing.T) {
	t.Parallel()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newTestBreaker(t, clk, Config{
		FailureThreshold:  3,
		MinimumRequests:   3,
		WindowDuration:    10 * time.Second,
		WindowSize:        10,
		OpenInterval:      30 * time.Second,
		HalfOpenMaxProbes: 1,
	})

	for i := 0; i < 3; i++ {
		err := b.Execute(func() error { return errors.New("boom") })
		if !errors.Is(err, errors.New("boom")) && err.Error() != "boom" {
			t.Fatalf("attempt %d err = %v", i, err)
		}
	}
	if got := b.State(); got != StateOpen {
		t.Fatalf("after 3 failures: state = %v, want open", got)
	}

	// Next Allow short-circuits.
	allowed, _ := b.Allow()
	if allowed {
		t.Error("open breaker must not allow")
	}
	if !errors.Is(b.Execute(func() error { return nil }), ErrOpen) {
		t.Error("Execute on open breaker must return ErrOpen")
	}
}

func TestBreaker_MinimumRequestsFloor(t *testing.T) {
	t.Parallel()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newTestBreaker(t, clk, Config{
		FailureThreshold:  3,
		MinimumRequests:   10,
		WindowDuration:    10 * time.Second,
		WindowSize:        10,
		OpenInterval:      30 * time.Second,
		HalfOpenMaxProbes: 1,
	})

	// 5 failures, but MinimumRequests=10 — must NOT trip yet.
	for i := 0; i < 5; i++ {
		_ = b.Execute(func() error { return errors.New("x") })
	}
	if got := b.State(); got != StateClosed {
		t.Errorf("below floor: state = %v, want closed", got)
	}

	// 5 more successes brings total to 10; the last failure that
	// rolled past the threshold already happened, so the next
	// failure re-evaluates and trips.
	for i := 0; i < 5; i++ {
		_ = b.Execute(func() error { return nil })
	}
	if got := b.State(); got != StateClosed {
		t.Errorf("at floor with only 5 failures: state = %v, want closed", got)
	}

	// 11th request is a failure — totals now 6/11, trip condition
	// (fails >= 3 && reqs >= 10) holds.
	_ = b.Execute(func() error { return errors.New("x") })
	if got := b.State(); got != StateOpen {
		t.Errorf("after floor crossed with 6 failures: state = %v, want open", got)
	}
}

func TestBreaker_OpenToHalfOpenToClosed(t *testing.T) {
	t.Parallel()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newTestBreaker(t, clk, Config{
		FailureThreshold:  2,
		MinimumRequests:   2,
		WindowDuration:    10 * time.Second,
		WindowSize:        10,
		OpenInterval:      30 * time.Second,
		HalfOpenMaxProbes: 1,
	})

	// Trip.
	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errors.New("x") })
	}
	if got := b.State(); got != StateOpen {
		t.Fatalf("setup: state = %v, want open", got)
	}

	// Within OpenInterval: short-circuit.
	clk.Advance(10 * time.Second)
	if err := b.Execute(func() error { return nil }); !errors.Is(err, ErrOpen) {
		t.Errorf("within open interval: err = %v, want ErrOpen", err)
	}

	// After OpenInterval: probe is allowed; success closes the breaker.
	clk.Advance(25 * time.Second) // total 35s elapsed, past 30s OpenInterval
	if err := b.Execute(func() error { return nil }); err != nil {
		t.Errorf("probe should pass: %v", err)
	}
	if got := b.State(); got != StateClosed {
		t.Errorf("after successful probe: state = %v, want closed", got)
	}
}

func TestBreaker_HalfOpenProbeFailureReopens(t *testing.T) {
	t.Parallel()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newTestBreaker(t, clk, Config{
		FailureThreshold:  2,
		MinimumRequests:   2,
		WindowDuration:    10 * time.Second,
		WindowSize:        10,
		OpenInterval:      30 * time.Second,
		HalfOpenMaxProbes: 1,
	})
	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errors.New("x") })
	}
	clk.Advance(31 * time.Second)

	if err := b.Execute(func() error { return errors.New("still bad") }); err == nil || errors.Is(err, ErrOpen) {
		t.Errorf("probe should call fn and return its err: %v", err)
	}
	if got := b.State(); got != StateOpen {
		t.Errorf("after failed probe: state = %v, want open", got)
	}

	// Cool-down restarted: short-circuit again.
	clk.Advance(10 * time.Second)
	if err := b.Execute(func() error { return nil }); !errors.Is(err, ErrOpen) {
		t.Errorf("re-cooldown should short-circuit: %v", err)
	}
}

func TestBreaker_HalfOpenLimitsConcurrentProbes(t *testing.T) {
	t.Parallel()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newTestBreaker(t, clk, Config{
		FailureThreshold:  2,
		MinimumRequests:   2,
		WindowDuration:    10 * time.Second,
		WindowSize:        10,
		OpenInterval:      30 * time.Second,
		HalfOpenMaxProbes: 2,
	})
	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errors.New("x") })
	}
	clk.Advance(31 * time.Second)

	// First two Allows pass — they fill HalfOpenMaxProbes.
	allowed1, done1 := b.Allow()
	allowed2, done2 := b.Allow()
	if !allowed1 || !allowed2 {
		t.Fatalf("first two half-open probes must be allowed: %v %v", allowed1, allowed2)
	}
	// Third must be short-circuited.
	allowed3, _ := b.Allow()
	if allowed3 {
		t.Errorf("third concurrent probe must be denied")
	}

	// Both probes succeed → breaker closes.
	done1(true)
	done2(true)
	if got := b.State(); got != StateClosed {
		t.Errorf("after all probes succeed: state = %v, want closed", got)
	}
}

func TestBreaker_HalfOpenRequiresAllProbesToSucceed(t *testing.T) {
	t.Parallel()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newTestBreaker(t, clk, Config{
		FailureThreshold:  2,
		MinimumRequests:   2,
		WindowDuration:    10 * time.Second,
		WindowSize:        10,
		OpenInterval:      30 * time.Second,
		HalfOpenMaxProbes: 3,
	})
	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errors.New("x") })
	}
	clk.Advance(31 * time.Second)

	a1, d1 := b.Allow()
	a2, d2 := b.Allow()
	if !a1 || !a2 {
		t.Fatalf("setup: probes not allowed")
	}
	d1(true)
	d2(true)
	// 2 of 3 probes done but no third yet: must still be half-open.
	if got := b.State(); got != StateHalfOpen {
		t.Errorf("after 2/3 successes: state = %v, want half_open", got)
	}
	a3, d3 := b.Allow()
	if !a3 {
		t.Fatalf("third probe must be allowed")
	}
	d3(true)
	if got := b.State(); got != StateClosed {
		t.Errorf("after 3/3 successes: state = %v, want closed", got)
	}
}

func TestBreaker_DoneCalledTwiceIsNoop(t *testing.T) {
	t.Parallel()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newTestBreaker(t, clk, Config{
		FailureThreshold: 1,
		MinimumRequests:  1,
		WindowDuration:   10 * time.Second,
		WindowSize:       10,
		OpenInterval:     30 * time.Second,
	})
	allowed, done := b.Allow()
	if !allowed {
		t.Fatal("Allow denied")
	}
	done(true)
	// Second call must not panic and must not flip state.
	done(false)
	if got := b.State(); got != StateClosed {
		t.Errorf("second done call mutated state: %v", got)
	}
}

func TestBreaker_StaleDoneAfterTransitionIgnored(t *testing.T) {
	t.Parallel()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newTestBreaker(t, clk, Config{
		FailureThreshold:  2,
		MinimumRequests:   2,
		WindowDuration:    10 * time.Second,
		WindowSize:        10,
		OpenInterval:      30 * time.Second,
		HalfOpenMaxProbes: 1,
	})
	// Get an in-flight Allow in closed state.
	allowed, done := b.Allow()
	if !allowed {
		t.Fatal("setup")
	}
	// Force a state rotation: two synchronous failures trip the
	// breaker; then jump time to half-open; succeed; back to closed.
	// At that point the original `done` carries the stale generation.
	for i := 0; i < 2; i++ {
		_ = b.Execute(func() error { return errors.New("x") })
	}
	clk.Advance(31 * time.Second)
	_ = b.Execute(func() error { return nil })

	if got := b.State(); got != StateClosed {
		t.Fatalf("setup: state = %v, want closed", got)
	}
	// Now fire the stale done(false): must NOT trip the breaker.
	done(false)
	if got := b.State(); got != StateClosed {
		t.Errorf("stale done flipped state to %v", got)
	}
}

func TestBreaker_DefaultIsFailureRespectsContextCanceled(t *testing.T) {
	t.Parallel()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newTestBreaker(t, clk, Config{
		FailureThreshold:  3,
		MinimumRequests:   3,
		WindowDuration:    10 * time.Second,
		WindowSize:        10,
		OpenInterval:      30 * time.Second,
		HalfOpenMaxProbes: 1,
	})
	for i := 0; i < 10; i++ {
		_ = b.Execute(func() error { return context.Canceled })
	}
	if got := b.State(); got != StateClosed {
		t.Errorf("context.Canceled counted as failure: state = %v", got)
	}
}

func TestBreaker_ConcurrentAllowSafe(t *testing.T) {
	t.Parallel()
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	b := newTestBreaker(t, clk, Config{
		FailureThreshold:  100,
		MinimumRequests:   100,
		WindowDuration:    10 * time.Second,
		WindowSize:        10,
		OpenInterval:      30 * time.Second,
		HalfOpenMaxProbes: 1,
	})
	var wg sync.WaitGroup
	var ok atomic.Int64
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if err := b.Execute(func() error { return nil }); err == nil {
					ok.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 5000 {
		t.Errorf("ok = %d, want 5000", ok.Load())
	}
}
