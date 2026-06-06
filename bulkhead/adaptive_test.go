package bulkhead

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAIMD_NoTrafficHoldsCapacity(t *testing.T) {
	t.Parallel()
	c := &AIMDController{}
	got := c.Next(Snapshot{Capacity: 20, Latency: LatencyStats{Count: 0}})
	if got != 20 {
		t.Errorf("no traffic: got %d, want 20", got)
	}
}

func TestAIMD_HealthyTickIncreases(t *testing.T) {
	t.Parallel()
	c := &AIMDController{IncreaseStep: 2}
	got := c.Next(Snapshot{
		Capacity:  10,
		Latency:   LatencyStats{Count: 50},
		ErrorRate: 0.02,
	})
	if got != 12 {
		t.Errorf("healthy: got %d, want 12", got)
	}
}

func TestAIMD_ErrorSpikeHalves(t *testing.T) {
	t.Parallel()
	c := &AIMDController{}
	got := c.Next(Snapshot{
		Capacity:  20,
		Latency:   LatencyStats{Count: 100},
		ErrorRate: 0.5,
	})
	if got != 10 {
		t.Errorf("error spike: got %d, want 10", got)
	}
}

func TestAIMD_FloorAtOne(t *testing.T) {
	t.Parallel()
	c := &AIMDController{DecreaseFactor: 0.1}
	got := c.Next(Snapshot{
		Capacity:  2,
		Latency:   LatencyStats{Count: 10},
		ErrorRate: 1.0,
	})
	if got != 1 {
		t.Errorf("floor: got %d, want 1", got)
	}
}

// ── Vegas controller ────────────────────────────────────────────────

func TestVegas_NoTrafficHoldsCapacity(t *testing.T) {
	t.Parallel()
	c := &VegasController{}
	got := c.Next(Snapshot{Capacity: 20, Latency: LatencyStats{Count: 0}})
	if got != 20 {
		t.Errorf("no traffic: got %d, want 20", got)
	}
}

func TestVegas_BaselineLearnedFromFirstTick_Grows(t *testing.T) {
	t.Parallel()
	// First tick: P50 = base by definition (no prior history), so
	// queueSize ≈ 0 < Alpha → +1. The baseline gets learned from this
	// very tick and the next tick at the same latency stays in the
	// grow regime.
	c := &VegasController{}
	got := c.Next(Snapshot{
		Capacity:  10,
		Latency:   LatencyStats{P50: 20 * time.Millisecond, Count: 50},
		ErrorRate: 0.02,
	})
	if got != 11 {
		t.Errorf("first healthy tick: got %d, want 11 (cap+1)", got)
	}
}

func TestVegas_LatencyExplosionShrinks(t *testing.T) {
	t.Parallel()
	c := &VegasController{}
	// Prime the baseline at 10ms — one healthy tick.
	c.Next(Snapshot{
		Capacity: 20,
		Latency:  LatencyStats{P50: 10 * time.Millisecond, Count: 100},
	})
	// Now P50 jumps to 100ms — 10× baseline → estimated = cap/10 = 2,
	// queueSize = 18, well above default Beta=6 → -1.
	got := c.Next(Snapshot{
		Capacity: 20,
		Latency:  LatencyStats{P50: 100 * time.Millisecond, Count: 100},
	})
	if got != 19 {
		t.Errorf("latency explosion: got %d, want 19 (cap-1)", got)
	}
}

func TestVegas_HoldsInSweetSpot(t *testing.T) {
	t.Parallel()
	c := &VegasController{Alpha: 2, Beta: 6}
	// Prime baseline at 10ms.
	c.Next(Snapshot{
		Capacity: 10,
		Latency:  LatencyStats{P50: 10 * time.Millisecond, Count: 50},
	})
	// P50 = 2× baseline → estimated = 5, queueSize = 5 — in (Alpha=2,
	// Beta=6] → hold.
	got := c.Next(Snapshot{
		Capacity: 10,
		Latency:  LatencyStats{P50: 20 * time.Millisecond, Count: 50},
	})
	if got != 10 {
		t.Errorf("sweet spot: got %d, want 10 (hold)", got)
	}
}

func TestVegas_ErrorSpikeMultiplicativeCut(t *testing.T) {
	t.Parallel()
	c := &VegasController{}
	// Even if latency looks fine, error rate above threshold halves.
	got := c.Next(Snapshot{
		Capacity:  20,
		Latency:   LatencyStats{P50: 10 * time.Millisecond, Count: 100},
		ErrorRate: 0.5,
	})
	if got != 10 {
		t.Errorf("error spike: got %d, want 10 (cap/2)", got)
	}
}

func TestVegas_ErrorSpikeFloorAtOne(t *testing.T) {
	t.Parallel()
	c := &VegasController{}
	got := c.Next(Snapshot{
		Capacity:  1,
		Latency:   LatencyStats{P50: 10 * time.Millisecond, Count: 10},
		ErrorRate: 1.0,
	})
	if got != 1 {
		t.Errorf("floor: got %d, want 1", got)
	}
}

func TestVegas_BaselineMonotonicallyDescends(t *testing.T) {
	t.Parallel()
	c := &VegasController{}
	// Prime at 20ms.
	c.Next(Snapshot{
		Capacity: 10,
		Latency:  LatencyStats{P50: 20 * time.Millisecond, Count: 50},
	})
	// A 5ms sample lowers the baseline to 5ms.
	c.Next(Snapshot{
		Capacity: 10,
		Latency:  LatencyStats{P50: 5 * time.Millisecond, Count: 50},
	})
	// Now a tick back at 20ms should look loaded relative to the
	// 5ms baseline: estimated = 10*5/20 = 2, queueSize = 8 > Beta=6 → -1.
	got := c.Next(Snapshot{
		Capacity: 10,
		Latency:  LatencyStats{P50: 20 * time.Millisecond, Count: 50},
	})
	if got != 9 {
		t.Errorf("loaded vs lower baseline: got %d, want 9 (cap-1)", got)
	}
}

func TestLatencyWindow_RecordsAndExpires(t *testing.T) {
	t.Parallel()
	w := newLatencyWindow(50 * time.Millisecond)

	w.record(10*time.Millisecond, true)
	w.record(20*time.Millisecond, false)

	s := w.stats()
	if s.Lat.Count != 2 {
		t.Errorf("count = %d, want 2", s.Lat.Count)
	}
	if s.Err != 0.5 {
		t.Errorf("err rate = %v, want 0.5", s.Err)
	}

	// Let entries expire.
	time.Sleep(70 * time.Millisecond)
	s = w.stats()
	if s.Lat.Count != 0 {
		t.Errorf("after expiry: count = %d, want 0", s.Lat.Count)
	}
}

func TestSetCapacity_RaiseWakesWaiters(t *testing.T) {
	t.Parallel()
	b, err := New(Config{Name: "test", MaxConcurrent: 1, MaxQueue: 5})
	if err != nil {
		t.Fatal(err)
	}

	r, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer r()

	// Park a waiter.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r2, err := b.Acquire(context.Background())
		if err != nil {
			t.Errorf("waiter Acquire: %v", err)
			return
		}
		r2()
	}()

	// Wait until parked.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if b.Stats().Waiting == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if b.Stats().Waiting != 1 {
		t.Fatal("waiter not parked")
	}

	// Raising the cap should immediately wake the waiter.
	b.SetCapacity(2)
	wg.Wait()
	if got := b.Stats().Capacity; got != 2 {
		t.Errorf("Capacity = %d, want 2", got)
	}
}

func TestSetCapacity_LowerDoesNotPreemptInFlight(t *testing.T) {
	t.Parallel()
	b, err := New(Config{Name: "test", MaxConcurrent: 3, MaxQueue: 0})
	if err != nil {
		t.Fatal(err)
	}

	r1, _ := b.Acquire(context.Background())
	r2, _ := b.Acquire(context.Background())
	r3, _ := b.Acquire(context.Background())

	// Drop cap below current in-flight.
	b.SetCapacity(1)

	if got := b.Stats().InFlight; got != 3 {
		t.Errorf("InFlight = %d, want 3 (existing slots preserved)", got)
	}
	// New Acquire must fail-fast (no queue).
	if _, err := b.Acquire(context.Background()); !errors.Is(err, ErrBulkheadFull) {
		t.Errorf("new Acquire: want ErrBulkheadFull, got %v", err)
	}

	r1()
	r2()
	r3()
}

func TestAdaptive_ValidationRejectsMissingController(t *testing.T) {
	t.Parallel()
	_, err := New(Config{Name: "x"}, WithAdaptive(AdaptiveConfig{
		InitialCap:  5,
		MinCapacity: 1,
		MaxCapacity: 10,
	}))
	if err == nil {
		t.Fatal("expected error for missing Controller")
	}
	var be *Error
	if !errors.As(err, &be) || be.Code != CodeInvalidAdaptiveConfig {
		t.Errorf("got %v, want CodeInvalidAdaptiveConfig", err)
	}
}

func TestAdaptive_RejectsMaxConcurrentWithAdaptive(t *testing.T) {
	t.Parallel()
	_, err := New(
		Config{Name: "x", MaxConcurrent: 5},
		WithAdaptive(AdaptiveConfig{
			Controller:  &AIMDController{},
			InitialCap:  5,
			MinCapacity: 1,
			MaxCapacity: 10,
		}),
	)
	if err == nil {
		t.Fatal("expected error for MaxConcurrent + WithAdaptive")
	}
}

func TestAdaptive_TickGrowsCapacityOnHealthyTraffic(t *testing.T) {
	t.Parallel()
	b, err := New(Config{Name: "test", MaxQueue: 100},
		WithAdaptive(AdaptiveConfig{
			Controller:   &AIMDController{IncreaseStep: 2},
			InitialCap:   2,
			MinCapacity:  1,
			MaxCapacity:  50,
			TickInterval: 20 * time.Millisecond,
			WindowSize:   200 * time.Millisecond,
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	// Generate healthy traffic.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = b.Execute(context.Background(), func() error {
					time.Sleep(2 * time.Millisecond)
					return nil
				})
			}
		}()
	}

	// Allow a handful of ticks.
	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()

	cap := b.Stats().Capacity
	if cap <= 2 {
		t.Errorf("capacity = %d, want > 2 (AIMD should have grown it)", cap)
	}
	if cap > 50 {
		t.Errorf("capacity = %d, want <= MaxCapacity=50", cap)
	}
}

func TestAdaptive_TickShrinksCapacityOnErrors(t *testing.T) {
	t.Parallel()
	b, err := New(Config{Name: "test", MaxQueue: 100},
		WithAdaptive(AdaptiveConfig{
			Controller:   &AIMDController{ErrorThreshold: 0.1},
			InitialCap:   20,
			MinCapacity:  1,
			MaxCapacity:  50,
			TickInterval: 20 * time.Millisecond,
			WindowSize:   200 * time.Millisecond,
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	// All-error traffic: Execute's release feeds (fn.err == nil) into
	// the latency window, so the controller sees 100% failure rate.
	boom := errors.New("boom")
	for i := 0; i < 50; i++ {
		_ = b.Execute(context.Background(), func() error { return boom })
	}

	// One tick must fire and observe the error rate.
	time.Sleep(60 * time.Millisecond)

	cap := b.Stats().Capacity
	if cap >= 20 {
		t.Errorf("capacity = %d, want < 20 (AIMD should have halved it)", cap)
	}
}

func TestAdaptive_CloseStopsTickGoroutine(t *testing.T) {
	t.Parallel()
	b, err := New(Config{Name: "test", MaxQueue: 1},
		WithAdaptive(AdaptiveConfig{
			Controller:   &AIMDController{},
			InitialCap:   5,
			MinCapacity:  1,
			MaxCapacity:  10,
			TickInterval: 10 * time.Millisecond,
		}))
	if err != nil {
		t.Fatal(err)
	}

	// Close should cleanly join the goroutine.
	done := make(chan struct{})
	go func() {
		b.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not return within 1s — tick goroutine leak")
	}

	// Idempotent.
	b.Close()
}

func TestAdaptive_ConcurrentAcquireUnderTickLoop(t *testing.T) {
	t.Parallel()
	b, err := New(Config{Name: "test", MaxQueue: 1000},
		WithAdaptive(AdaptiveConfig{
			Controller:   &AIMDController{},
			InitialCap:   5,
			MinCapacity:  1,
			MaxCapacity:  20,
			TickInterval: 10 * time.Millisecond,
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	const goroutines = 50
	const opsEach = 100
	var ok atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < opsEach; i++ {
				if err := b.Execute(context.Background(), func() error {
					return nil
				}); err == nil {
					ok.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if ok.Load() != goroutines*opsEach {
		t.Errorf("ok = %d, want %d", ok.Load(), goroutines*opsEach)
	}
	// Capacity must remain bounded.
	cap := b.Stats().Capacity
	if cap < 1 || cap > 20 {
		t.Errorf("capacity = %d, want in [1,20]", cap)
	}
}
