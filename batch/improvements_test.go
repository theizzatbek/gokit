package batch

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── A. Panic recovery ─────────────────────────────────────────────────

func TestBatcher_HandlerPanicRecovered(t *testing.T) {
	var ackErrMu sync.Mutex
	var ackErr error
	ackDone := make(chan struct{}, 1)

	b, err := New(Config[int]{
		HandlerFn: func(context.Context, []int) error { panic("boom") },
		BatchSize: 2,
		Interval:  10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })

	b.Submit(1, func(e error) {
		ackErrMu.Lock()
		ackErr = e
		ackErrMu.Unlock()
		select {
		case ackDone <- struct{}{}:
		default:
		}
	})
	b.Submit(2, nil) // triggers size flush

	select {
	case <-ackDone:
	case <-time.After(time.Second):
		t.Fatal("ack never fired — flushLoop may have died")
	}

	ackErrMu.Lock()
	got := ackErr
	ackErrMu.Unlock()
	if got == nil {
		t.Error("ack received nil err; want recovered panic")
	}

	// Verify the batcher is STILL working — a second batch should
	// dispatch normally.
	var ok atomic.Bool
	b.Submit(3, func(e error) {
		if e != nil {
			t.Logf("second batch err = %v", e)
		} else {
			ok.Store(true)
		}
	})
	b.Submit(4, nil)
	time.Sleep(50 * time.Millisecond)
	// Second batch also panics by the same HandlerFn — but flushLoop
	// must still be alive. We assert "ack fired" rather than "no
	// error" here.
	if s := b.Stats(); s.FailedHandlers < 2 {
		t.Errorf("FailedHandlers = %d, want >= 2 (flushLoop alive)", s.FailedHandlers)
	}
}

// ── D. Backpressure ──────────────────────────────────────────────────

func TestBatcher_TrySubmit_PendingFull(t *testing.T) {
	// Slow handler so pending fills before drain.
	release := make(chan struct{})
	b, _ := New(Config[int]{
		HandlerFn: func(context.Context, []int) error {
			<-release
			return nil
		},
		BatchSize:  10,
		Interval:   time.Second,
		MaxPending: 5,
	})
	t.Cleanup(func() {
		close(release)
		_ = b.Close()
	})

	// First batch triggers a slow handler — pending should fill on
	// next 5 items. But pending is reset after dispatch swap;
	// MaxPending applies to the live buffer between flushes.
	for i := 0; i < 5; i++ {
		if err := b.TrySubmit(i, nil); err != nil {
			t.Fatalf("submit %d should succeed: %v", i, err)
		}
	}
	// 6th submit MAY block on the BatchSize trigger flush which
	// dispatches the first 10 — but we don't reach BatchSize. Pending
	// is 5/5 — next TrySubmit must fail.
	if err := b.TrySubmit(99, nil); !errors.Is(err, ErrPendingFull) {
		t.Errorf("expected ErrPendingFull on overflow, got %v", err)
	}
}

func TestBatcher_Submit_PendingFull_AcksWithErr(t *testing.T) {
	release := make(chan struct{})
	b, _ := New(Config[int]{
		HandlerFn: func(context.Context, []int) error {
			<-release
			return nil
		},
		BatchSize:  10,
		Interval:   time.Second,
		MaxPending: 2,
	})
	t.Cleanup(func() {
		close(release)
		_ = b.Close()
	})

	_ = b.TrySubmit(1, nil)
	_ = b.TrySubmit(2, nil)

	var mu sync.Mutex
	var ackErr error
	done := make(chan struct{}, 1)
	b.Submit(3, func(e error) {
		mu.Lock()
		ackErr = e
		mu.Unlock()
		done <- struct{}{}
	})
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ack never fired on full-buffer submit")
	}
	mu.Lock()
	got := ackErr
	mu.Unlock()
	if !errors.Is(got, ErrPendingFull) {
		t.Errorf("ack err = %v, want ErrPendingFull", got)
	}
}

// ── B. Worker pool ───────────────────────────────────────────────────

func TestBatcher_MaxInFlightHandlers_RunsParallel(t *testing.T) {
	var concurrent atomic.Int32
	var peak atomic.Int32

	b, _ := New(Config[int]{
		HandlerFn: func(context.Context, []int) error {
			n := concurrent.Add(1)
			defer concurrent.Add(-1)
			for {
				cur := peak.Load()
				if n <= cur || peak.CompareAndSwap(cur, n) {
					break
				}
			}
			time.Sleep(80 * time.Millisecond)
			return nil
		},
		BatchSize:           5,
		Interval:            time.Hour, // disable interval; tests drive via Flush
		MaxInFlightHandlers: 4,
	})
	t.Cleanup(func() { _ = b.Close() })

	// Spawn 4 dispatches back-to-back via explicit Flush calls so
	// the pool gets a chance to parallelise. Each Flush spawns a
	// dispatch goroutine when MaxInFlightHandlers > 1.
	for batch := 0; batch < 4; batch++ {
		for i := 0; i < 5; i++ {
			b.Submit(0, nil)
		}
		// Force a flush so the pending buffer rotates into a fresh
		// dispatch goroutine before the next batch fills.
		_ = b.Flush(context.Background())
	}
	time.Sleep(150 * time.Millisecond)

	if got := peak.Load(); got < 2 {
		t.Errorf("peak concurrent handlers = %d, want >= 2 (pool active)", got)
	}
}

// ── C. Retry policy ──────────────────────────────────────────────────

func TestBatcher_RetryOnError_SucceedsEventually(t *testing.T) {
	var calls atomic.Int32
	var ackErrMu sync.Mutex
	var ackErr error
	done := make(chan struct{}, 1)

	b, _ := New(Config[int]{
		HandlerFn: func(context.Context, []int) error {
			n := calls.Add(1)
			if n < 3 {
				return errors.New("transient")
			}
			return nil
		},
		BatchSize:        1,
		Interval:         time.Second,
		MaxRetries:       5,
		RetryBackoffBase: time.Millisecond,
		RetryBackoffMax:  10 * time.Millisecond,
	})
	t.Cleanup(func() { _ = b.Close() })

	b.Submit(1, func(e error) {
		ackErrMu.Lock()
		ackErr = e
		ackErrMu.Unlock()
		done <- struct{}{}
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ack never fired")
	}
	ackErrMu.Lock()
	got := ackErr
	ackErrMu.Unlock()
	if got != nil {
		t.Errorf("final ack err = %v, want nil (third attempt succeeds)", got)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
	if s := b.Stats(); s.RetriedAttempts < 2 {
		t.Errorf("RetriedAttempts = %d, want >= 2", s.RetriedAttempts)
	}
}

func TestBatcher_DefaultClassifier_CtxCanceled_NoRetries(t *testing.T) {
	var calls atomic.Int32
	done := make(chan error, 1)

	b, _ := New(Config[int]{
		HandlerFn: func(context.Context, []int) error {
			calls.Add(1)
			return context.Canceled
		},
		BatchSize:        1,
		Interval:         time.Second,
		MaxRetries:       5,
		RetryBackoffBase: time.Millisecond,
		RetryBackoffMax:  10 * time.Millisecond,
	})
	t.Cleanup(func() { _ = b.Close() })

	b.Submit(1, func(e error) { done <- e })

	select {
	case got := <-done:
		if !errors.Is(got, context.Canceled) {
			t.Errorf("ack err = %v, want context.Canceled", got)
		}
	case <-time.After(time.Second):
		t.Fatal("ack never fired")
	}
	// Default classifier must skip retries on ctx.Canceled — only
	// the initial attempt should have fired.
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1 (no retries on ctx.Canceled)", got)
	}
	if s := b.Stats(); s.RetriedAttempts != 0 {
		t.Errorf("RetriedAttempts = %d, want 0", s.RetriedAttempts)
	}
}

func TestBatcher_CustomClassifier_BreaksRetryEarly(t *testing.T) {
	var calls atomic.Int32
	done := make(chan error, 1)
	permanent := errors.New("permanent")

	b, _ := New(Config[int]{
		HandlerFn: func(context.Context, []int) error {
			calls.Add(1)
			return permanent
		},
		BatchSize:        1,
		Interval:         time.Second,
		MaxRetries:       5,
		RetryBackoffBase: time.Millisecond,
		RetryBackoffMax:  10 * time.Millisecond,
		IsRetryable: func(err error) bool {
			// Caller marks this specific error as permanent.
			return !errors.Is(err, permanent)
		},
	})
	t.Cleanup(func() { _ = b.Close() })

	b.Submit(1, func(e error) { done <- e })

	select {
	case got := <-done:
		if !errors.Is(got, permanent) {
			t.Errorf("ack err = %v, want permanent", got)
		}
	case <-time.After(time.Second):
		t.Fatal("ack never fired")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1 (custom classifier broke retry early)", got)
	}
}

// ── E. Hooks ────────────────────────────────────────────────────────

func TestBatcher_OnBatchStartComplete_Fire(t *testing.T) {
	var startSize, completeSize atomic.Int32

	b, _ := New(Config[int]{
		HandlerFn: func(context.Context, []int) error { return nil },
		BatchSize: 3,
		Interval:  time.Second,
		OnBatchStart: func(_ context.Context, size int) {
			startSize.Store(int32(size))
		},
		OnBatchComplete: func(_ context.Context, size int, _ error, _ time.Duration) {
			completeSize.Store(int32(size))
		},
	})
	t.Cleanup(func() { _ = b.Close() })

	b.Submit(1, nil)
	b.Submit(2, nil)
	b.Submit(3, nil) // size flush
	time.Sleep(50 * time.Millisecond)

	if startSize.Load() != 3 {
		t.Errorf("OnBatchStart size = %d, want 3", startSize.Load())
	}
	if completeSize.Load() != 3 {
		t.Errorf("OnBatchComplete size = %d, want 3", completeSize.Load())
	}
}

func TestBatcher_HookPanicRecovered(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic escaped batcher: %v", r)
		}
	}()
	b, _ := New(Config[int]{
		HandlerFn:    func(context.Context, []int) error { return nil },
		BatchSize:    1,
		Interval:     time.Second,
		OnBatchStart: func(context.Context, int) { panic("hook boom") },
	})
	t.Cleanup(func() { _ = b.Close() })
	b.Submit(1, nil)
	time.Sleep(50 * time.Millisecond)
}

// ── H. Stats ─────────────────────────────────────────────────────────

func TestBatcher_Stats_ReportsCounters(t *testing.T) {
	b, _ := New(Config[int]{
		HandlerFn: func(context.Context, []int) error { return nil },
		BatchSize: 2,
		Interval:  time.Second,
	})
	t.Cleanup(func() { _ = b.Close() })

	b.Submit(1, nil)
	b.Submit(2, nil) // size flush
	time.Sleep(30 * time.Millisecond)

	s := b.Stats()
	if s.DispatchedTotal != 2 {
		t.Errorf("DispatchedTotal = %d, want 2", s.DispatchedTotal)
	}
	if s.FailedHandlers != 0 {
		t.Errorf("FailedHandlers = %d, want 0", s.FailedHandlers)
	}
}

func TestBatcher_StatsNilReceiverSafe(t *testing.T) {
	var b *Batcher[int]
	if got := b.Stats(); got.DispatchedTotal != 0 {
		t.Errorf("nil Stats = %+v, want zero", got)
	}
}
