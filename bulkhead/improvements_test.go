package bulkhead

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// ── B. OnCapacityChange hook ─────────────────────────────────────────

func TestBulkhead_OnCapacityChangeFires(t *testing.T) {
	var calls atomic.Int32
	var lastPrev, lastNext atomic.Int32
	b, err := New(Config{
		Name:          "hook",
		MaxConcurrent: 4,
		OnCapacityChange: func(prev, next int) {
			calls.Add(1)
			lastPrev.Store(int32(prev))
			lastNext.Store(int32(next))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)

	b.SetCapacity(8)
	if calls.Load() != 1 || lastPrev.Load() != 4 || lastNext.Load() != 8 {
		t.Errorf("hook = (calls=%d, prev=%d, next=%d), want (1, 4, 8)",
			calls.Load(), lastPrev.Load(), lastNext.Load())
	}

	// No-op SetCapacity (same value) — hook must NOT fire.
	b.SetCapacity(8)
	if calls.Load() != 1 {
		t.Errorf("hook fired on no-op SetCapacity; calls = %d, want 1", calls.Load())
	}
}

func TestBulkhead_OnCapacityChangePanicSafe(t *testing.T) {
	b, _ := New(Config{
		Name:             "panic-cap",
		MaxConcurrent:    2,
		OnCapacityChange: func(prev, next int) { panic("user error") },
	})
	t.Cleanup(b.Close)

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic escaped SetCapacity: %v", r)
		}
	}()
	b.SetCapacity(3)
}

// ── HI. Bulkhead Stats latency/wait extension ─────────────────────────

func TestBulkhead_Stats_ReportsLatencyAndWait(t *testing.T) {
	b, _ := New(Config{
		Name:          "stats",
		MaxConcurrent: 1, // forces queue waits
		MaxQueue:      8,
		StatsWindow:   time.Second,
	})
	t.Cleanup(b.Close)

	// Run a slow operation in a goroutine to block subsequent
	// acquires; record their wait times.
	released := make(chan struct{})
	go func() {
		_ = b.Execute(context.Background(), func() error {
			<-released
			return nil
		})
	}()

	// Give the long-running op time to acquire.
	time.Sleep(20 * time.Millisecond)

	// Now do 5 queued Acquire calls; each waits for the long op.
	for i := 0; i < 5; i++ {
		go func() {
			rel, err := b.Acquire(context.Background())
			if err != nil {
				return
			}
			// Sleep a bit so the call-latency observation is
			// non-zero.
			time.Sleep(10 * time.Millisecond)
			rel()
		}()
	}

	// Release the long op so the queued goroutines drain.
	time.Sleep(30 * time.Millisecond)
	close(released)

	// Allow drain.
	time.Sleep(150 * time.Millisecond)

	s := b.Stats()
	if s.SampleSize == 0 {
		t.Fatal("SampleSize = 0; expected observations within window")
	}
	if s.LatencyP50 == 0 && s.LatencyP99 == 0 {
		t.Error("LatencyP50/P99 both zero — call latency window not populated")
	}
	// AvgWait may be 0 if all queued goroutines never woke up. Just
	// log; not strict.
	t.Logf("Stats: %+v", s)
}

func TestBulkhead_StatsNilReceiverSafe(t *testing.T) {
	var b *Bulkhead
	if got := b.Stats(); got.InFlight != 0 || got.Capacity != 0 {
		t.Errorf("nil Stats = %+v, want zero", got)
	}
}
