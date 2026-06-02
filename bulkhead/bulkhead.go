package bulkhead

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Outcome labels used in metrics + log fields.
const (
	outcomeOK           = "ok"
	outcomeFull         = "full"
	outcomeCtxCanceled  = "ctx_canceled"
	outcomeQueueTimeout = "queue_timeout"
)

// Bulkhead caps concurrent calls against a single upstream and
// optionally queues a bounded number of extra callers. Construct via
// [New]. (*Bulkhead)(nil) is a no-op receiver: Acquire always succeeds
// with a noop release, Execute just runs fn. This lets adapters thread
// an optional bulkhead through code unconditionally.
//
// Internally the semaphore is a mutex + cond + integer counter rather
// than a buffered channel — the channel-based primitive cannot resize
// in place, which is required for [WithAdaptive] / [Bulkhead.SetCapacity].
type Bulkhead struct {
	mu       sync.Mutex
	cond     *sync.Cond
	inflight int
	capacity int
	waiting  int

	maxQueue     int
	queueTimeout time.Duration
	cfg          Config
	collector    *metricsCollector

	adaptive  *adaptiveState // nil when static
	closeOnce sync.Once
}

// Stats is the point-in-time snapshot returned by [Bulkhead.Stats].
// Cheap (one mutex acquire). Suitable for /healthz / admin endpoints.
type Stats struct {
	InFlight int
	Waiting  int
	Capacity int
}

// New validates cfg and returns a bulkhead with every slot free.
// Returns *Error (wrapping a stable Code constant) on invalid config.
func New(cfg Config, opts ...Option) (*Bulkhead, error) {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	if err := validateNew(cfg, o); err != nil {
		return nil, err
	}
	b := &Bulkhead{
		cfg:          cfg,
		maxQueue:     cfg.MaxQueue,
		queueTimeout: cfg.QueueTimeout,
	}
	b.cond = sync.NewCond(&b.mu)
	// Initial capacity: static cfg or adaptive InitialCap.
	if o.adaptive != nil {
		b.capacity = o.adaptive.InitialCap
	} else {
		b.capacity = cfg.MaxConcurrent
	}
	if cfg.Metrics != nil {
		b.collector = newMetricsCollector(cfg.Metrics, cfg.Name,
			func() float64 {
				b.mu.Lock()
				defer b.mu.Unlock()
				return float64(b.inflight)
			},
			func() float64 {
				b.mu.Lock()
				defer b.mu.Unlock()
				return float64(b.waiting)
			},
			func() float64 {
				b.mu.Lock()
				defer b.mu.Unlock()
				return float64(b.capacity)
			},
		)
	}
	if o.adaptive != nil {
		b.adaptive = newAdaptiveState(*o.adaptive)
		go b.adaptive.loop(b)
	}
	return b, nil
}

// Stats returns the current in-flight / waiting / capacity snapshot.
// Nil receiver returns the zero value.
func (b *Bulkhead) Stats() Stats {
	if b == nil {
		return Stats{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return Stats{
		InFlight: b.inflight,
		Waiting:  b.waiting,
		Capacity: b.capacity,
	}
}

// SetCapacity sets the current MaxConcurrent target. n is clamped to
// >= 1. Raising the cap wakes parked waiters via cond.Broadcast;
// lowering it does NOT preempt in-flight calls — they finish
// naturally, and new Acquires block until inflight ≤ capacity again
// (the "drain on shrink" semantic).
//
// SetCapacity is the operator runbook lever: "the upstream is in
// incident; force cap down to N for the next 10 minutes." It is also
// what the adaptive tick loop calls internally — both paths route
// through one mutator.
//
// Nil receiver is a no-op.
func (b *Bulkhead) SetCapacity(n int) {
	if b == nil {
		return
	}
	if n < 1 {
		n = 1
	}
	b.mu.Lock()
	prev := b.capacity
	b.capacity = n
	if n > prev {
		b.cond.Broadcast()
	}
	b.mu.Unlock()
}

// Close stops the adaptive controller goroutine (if any) and is
// idempotent. Static bulkheads (no [WithAdaptive]) do not need to be
// Closed — Close on them is a no-op.
func (b *Bulkhead) Close() {
	if b == nil {
		return
	}
	b.closeOnce.Do(func() {
		if b.adaptive != nil {
			close(b.adaptive.stop)
			<-b.adaptive.done
		}
	})
}

// Acquire tries to grab a slot. Returns:
//
//   - (release, nil) on success. release MUST be called once when the
//     protected operation finishes (including on error). It is
//     idempotent — extra calls are no-ops.
//   - (nil, [ErrBulkheadFull]) when capacity + MaxQueue are saturated
//     (fast-fail; no waiting was attempted).
//   - (nil, ctx.Err()) when ctx is cancelled while waiting.
//   - (nil, [ErrQueueTimeout]) when QueueTimeout fires before a slot
//     frees.
//
// Nil receiver: returns (noopRelease, nil) so callers can thread an
// optional bulkhead without nil-checks.
func (b *Bulkhead) Acquire(ctx context.Context) (func(), error) {
	if b == nil {
		return noopRelease, nil
	}
	release, err := b.acquireInternal(ctx)
	if err != nil {
		return nil, err
	}
	// Acquire's release defaults to success=true. Callers needing to
	// feed a failure outcome into the adaptive latency window use
	// [Bulkhead.Execute] (which derives success from fn's error).
	return func() { release(true) }, nil
}

// acquireInternal is the shared path for Acquire and Execute. It
// returns a release closure that takes a success bool — Execute feeds
// fn's err==nil, Acquire wraps it with success=true.
func (b *Bulkhead) acquireInternal(ctx context.Context) (func(bool), error) {
	start := time.Now()

	b.mu.Lock()
	if b.inflight < b.capacity {
		b.inflight++
		b.mu.Unlock()
		b.collector.observe(outcomeOK, 0)
		return b.makeRelease(time.Now()), nil
	}
	if b.waiting >= b.maxQueue {
		b.mu.Unlock()
		b.collector.observe(outcomeFull, 0)
		return nil, ErrBulkheadFull
	}
	b.waiting++

	// Watchdog converts ctx/timer signals into a cond Broadcast —
	// sync.Cond.Wait does not select on channels.
	watchDone := make(chan struct{})
	var (
		cancelled bool
		timedOut  bool
	)
	go b.watchdog(ctx, watchDone, &cancelled, &timedOut)

	for b.inflight >= b.capacity && !cancelled && !timedOut {
		b.cond.Wait()
	}
	b.waiting--
	close(watchDone)

	if cancelled {
		b.mu.Unlock()
		b.collector.observe(outcomeCtxCanceled, time.Since(start))
		return nil, ctx.Err()
	}
	if timedOut {
		b.mu.Unlock()
		b.collector.observe(outcomeQueueTimeout, time.Since(start))
		return nil, ErrQueueTimeout
	}
	b.inflight++
	b.mu.Unlock()
	b.collector.observe(outcomeOK, time.Since(start))
	return b.makeRelease(time.Now()), nil
}

// watchdog runs while a goroutine is parked in cond.Wait. It owns the
// ctx + queueTimeout signal: whichever fires first flips the
// corresponding flag under mu and broadcasts so the parked waiter
// re-checks its predicate. watchDone signals the caller has resolved
// (acquired or failed) so the watchdog exits cleanly.
func (b *Bulkhead) watchdog(ctx context.Context, watchDone chan struct{}, cancelled, timedOut *bool) {
	var timerCh <-chan time.Time
	if b.queueTimeout > 0 {
		t := time.NewTimer(b.queueTimeout)
		defer t.Stop()
		timerCh = t.C
	}
	select {
	case <-ctx.Done():
		b.mu.Lock()
		*cancelled = true
		b.cond.Broadcast()
		b.mu.Unlock()
	case <-timerCh:
		b.mu.Lock()
		*timedOut = true
		b.cond.Broadcast()
		b.mu.Unlock()
	case <-watchDone:
		return
	}
}

// Execute is the ergonomic wrapper: Acquire + run + release. fn's
// error propagates as-is. The release feeds (err == nil) into the
// adaptive latency window as the success outcome — so when
// [WithAdaptive] is enabled the AIMD-style controller sees fn errors
// as failures without callers writing their own bookkeeping.
func (b *Bulkhead) Execute(ctx context.Context, fn func() error) error {
	if b == nil {
		return fn()
	}
	release, err := b.acquireInternal(ctx)
	if err != nil {
		return err
	}
	err = fn()
	release(err == nil)
	return err
}

// makeRelease returns the per-Acquire release closure. The closure
// takes a success bool — Acquire's wrapper supplies true; Execute
// supplies (fn err == nil). The atomic.Bool guard makes double-release
// a no-op so caller bugs do not leak slots.
func (b *Bulkhead) makeRelease(start time.Time) func(bool) {
	var released atomic.Bool
	return func(success bool) {
		if !released.CompareAndSwap(false, true) {
			return
		}
		b.releaseOne(start, success)
	}
}

// releaseOne is the internal release path. Mutates state under mu,
// records latency, signals one waiter.
func (b *Bulkhead) releaseOne(start time.Time, success bool) {
	b.mu.Lock()
	b.inflight--
	if b.adaptive != nil {
		b.adaptive.window.record(time.Since(start), success)
	}
	b.cond.Signal()
	b.mu.Unlock()
	b.collector.observeCallLatency(time.Since(start))
}

// noopRelease is the release returned by Acquire on a nil receiver.
// Safe to call any number of times.
func noopRelease() {}
