package bulkhead

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestBulkhead(t *testing.T, cfg Config) *Bulkhead {
	t.Helper()
	if cfg.Name == "" {
		cfg.Name = "test"
	}
	b, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func TestBulkhead_NilReceiverIsNoop(t *testing.T) {
	t.Parallel()
	var b *Bulkhead
	release, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatalf("nil Acquire: %v", err)
	}
	release()
	release() // double-call must not panic

	if err := b.Execute(context.Background(), func() error { return nil }); err != nil {
		t.Errorf("nil Execute: %v", err)
	}
	if got := b.Stats(); got != (Stats{}) {
		t.Errorf("nil Stats = %+v, want zero", got)
	}
}

func TestBulkhead_AcquireFastPath(t *testing.T) {
	t.Parallel()
	b := newTestBulkhead(t, Config{MaxConcurrent: 2})

	r1, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r2, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	s := b.Stats()
	if s.InFlight != 2 || s.Capacity != 2 {
		t.Errorf("stats = %+v, want InFlight=2 Capacity=2", s)
	}

	r1()
	r2()

	if got := b.Stats().InFlight; got != 0 {
		t.Errorf("after release: InFlight = %d, want 0", got)
	}
}

func TestBulkhead_FailFastWhenNoQueue(t *testing.T) {
	t.Parallel()
	b := newTestBulkhead(t, Config{MaxConcurrent: 1, MaxQueue: 0})

	release, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	// Second Acquire must fail-fast with ErrBulkheadFull.
	_, err = b.Acquire(context.Background())
	if !errors.Is(err, ErrBulkheadFull) {
		t.Errorf("want ErrBulkheadFull, got %v", err)
	}
}

func TestBulkhead_QueueLimit(t *testing.T) {
	t.Parallel()
	// MaxConcurrent=1, MaxQueue=2 → 1 in-flight + 2 waiters allowed;
	// the 4th caller gets ErrBulkheadFull.
	b := newTestBulkhead(t, Config{MaxConcurrent: 1, MaxQueue: 2})

	r1, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Two waiters: they should block, not error.
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			r, err := b.Acquire(context.Background())
			if err != nil {
				t.Errorf("queued waiter err: %v", err)
				return
			}
			r()
		}()
	}

	// Give the waiters a moment to register.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if b.Stats().Waiting >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := b.Stats().Waiting; got != 2 {
		t.Fatalf("waiting = %d, want 2", got)
	}

	// The 4th caller (no queue room) must fail fast.
	_, err = b.Acquire(context.Background())
	if !errors.Is(err, ErrBulkheadFull) {
		t.Errorf("4th caller: want ErrBulkheadFull, got %v", err)
	}

	r1()
	wg.Wait()

	if got := b.Stats(); got.InFlight != 0 || got.Waiting != 0 {
		t.Errorf("after drain: %+v, want zero", got)
	}
}

func TestBulkhead_CtxCancelMidWait(t *testing.T) {
	t.Parallel()
	b := newTestBulkhead(t, Config{MaxConcurrent: 1, MaxQueue: 5})

	r1, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer r1()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err = b.Acquire(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}
	if got := b.Stats().Waiting; got != 0 {
		t.Errorf("waiter counter not decremented: %d", got)
	}
}

func TestBulkhead_QueueTimeout(t *testing.T) {
	t.Parallel()
	b := newTestBulkhead(t, Config{
		MaxConcurrent: 1,
		MaxQueue:      5,
		QueueTimeout:  20 * time.Millisecond,
	})

	r1, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer r1()

	start := time.Now()
	_, err = b.Acquire(context.Background())
	elapsed := time.Since(start)
	if !errors.Is(err, ErrQueueTimeout) {
		t.Errorf("want ErrQueueTimeout, got %v", err)
	}
	if elapsed < 15*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Errorf("elapsed = %v, want ~20ms", elapsed)
	}
	if got := b.Stats().Waiting; got != 0 {
		t.Errorf("waiter counter not decremented: %d", got)
	}
}

func TestBulkhead_DoubleReleaseIsIdempotent(t *testing.T) {
	t.Parallel()
	b := newTestBulkhead(t, Config{MaxConcurrent: 1})
	release, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
	release() // must not panic and must not free a phantom slot

	// Confirm the slot is correctly accounted: re-acquire should work,
	// and a second Acquire should fail-fast (proving InFlight==1).
	r2, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	if _, err := b.Acquire(context.Background()); !errors.Is(err, ErrBulkheadFull) {
		t.Errorf("expected full after double-release re-acquire, got %v", err)
	}
	r2()
}

func TestBulkhead_Execute(t *testing.T) {
	t.Parallel()
	b := newTestBulkhead(t, Config{MaxConcurrent: 1})
	called := false
	if err := b.Execute(context.Background(), func() error {
		called = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("fn not called")
	}

	// fn's error propagates.
	want := errors.New("boom")
	if err := b.Execute(context.Background(), func() error { return want }); !errors.Is(err, want) {
		t.Errorf("Execute err = %v, want wrapping %v", err, want)
	}

	// Execute that can't acquire returns the Acquire error without
	// calling fn.
	r, _ := b.Acquire(context.Background())
	defer r()
	notCalled := true
	err := b.Execute(context.Background(), func() error {
		notCalled = false
		return nil
	})
	if !errors.Is(err, ErrBulkheadFull) {
		t.Errorf("Execute when full: %v", err)
	}
	if !notCalled {
		t.Error("fn called even though Acquire failed")
	}
}

func TestBulkhead_ConcurrentStress(t *testing.T) {
	t.Parallel()
	b := newTestBulkhead(t, Config{MaxConcurrent: 10, MaxQueue: 1000})

	const goroutines = 50
	const opsEach = 200

	var wg sync.WaitGroup
	var ok atomic.Int64
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
	if got := b.Stats(); got.InFlight != 0 || got.Waiting != 0 {
		t.Errorf("final stats = %+v, want zero", got)
	}
}

func TestBulkhead_MaxConcurrentEnforced(t *testing.T) {
	t.Parallel()
	const cap = 3
	b := newTestBulkhead(t, Config{MaxConcurrent: cap, MaxQueue: 1000})

	var peak atomic.Int64
	var inFlight atomic.Int64
	const callers = 30

	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = b.Execute(context.Background(), func() error {
				cur := inFlight.Add(1)
				defer inFlight.Add(-1)
				for {
					p := peak.Load()
					if cur <= p || peak.CompareAndSwap(p, cur) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				return nil
			})
		}()
	}
	wg.Wait()

	if peak.Load() > int64(cap) {
		t.Errorf("peak in-flight = %d, want <= %d", peak.Load(), cap)
	}
}
