package bulkhead

import (
	"context"
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
type Bulkhead struct {
	sem          chan struct{}
	waiting      atomic.Int64
	cfg          Config
	maxQueue     int
	queueTimeout time.Duration
	collector    *metricsCollector
}

// Stats is the point-in-time snapshot returned by [Bulkhead.Stats].
// Cheap to compute — InFlight reads len(chan) and Waiting reads an
// atomic. Suitable for /healthz / admin endpoints.
type Stats struct {
	InFlight int
	Waiting  int
	Capacity int
}

// New validates cfg and returns a bulkhead with every slot free.
// Returns *Error (wrapping a stable Code constant) on invalid config.
func New(cfg Config) (*Bulkhead, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	b := &Bulkhead{
		sem:          make(chan struct{}, cfg.MaxConcurrent),
		cfg:          cfg,
		maxQueue:     cfg.MaxQueue,
		queueTimeout: cfg.QueueTimeout,
	}
	if cfg.Metrics != nil {
		b.collector = newMetricsCollector(cfg.Metrics, cfg.Name,
			func() float64 { return float64(len(b.sem)) },
			func() float64 { return float64(b.waiting.Load()) },
		)
	}
	return b, nil
}

// Stats returns the current in-flight / waiting / capacity snapshot.
// Nil receiver returns the zero value.
func (b *Bulkhead) Stats() Stats {
	if b == nil {
		return Stats{}
	}
	return Stats{
		InFlight: len(b.sem),
		Waiting:  int(b.waiting.Load()),
		Capacity: cap(b.sem),
	}
}

// Acquire tries to grab a slot. Returns:
//
//   - (release, nil) on success. release MUST be called once when the
//     protected operation finishes (including on error). It is
//     idempotent — extra calls are no-ops.
//   - (nil, [ErrBulkheadFull]) when MaxConcurrent + MaxQueue are
//     saturated (fast-fail; no waiting was attempted).
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
	// Fast path: slot available without queuing.
	select {
	case b.sem <- struct{}{}:
		b.collector.observe(outcomeOK, 0)
		return b.makeRelease(), nil
	default:
	}
	// Queue path: check waiter cap before blocking.
	waiters := int(b.waiting.Add(1))
	if waiters > b.maxQueue {
		b.waiting.Add(-1)
		b.collector.observe(outcomeFull, 0)
		return nil, ErrBulkheadFull
	}
	var timerCh <-chan time.Time
	if b.queueTimeout > 0 {
		t := time.NewTimer(b.queueTimeout)
		defer t.Stop()
		timerCh = t.C
	}
	start := time.Now()
	select {
	case b.sem <- struct{}{}:
		b.waiting.Add(-1)
		b.collector.observe(outcomeOK, time.Since(start))
		return b.makeRelease(), nil
	case <-ctx.Done():
		b.waiting.Add(-1)
		b.collector.observe(outcomeCtxCanceled, time.Since(start))
		return nil, ctx.Err()
	case <-timerCh:
		b.waiting.Add(-1)
		b.collector.observe(outcomeQueueTimeout, time.Since(start))
		return nil, ErrQueueTimeout
	}
}

// Execute is the ergonomic wrapper: Acquire + run + release. If
// Acquire fails (full/timeout/cancelled), fn is NOT called and the
// error is returned. fn's error propagates as-is — bulkhead does not
// classify success vs failure (a slot is a slot).
func (b *Bulkhead) Execute(ctx context.Context, fn func() error) error {
	release, err := b.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

// makeRelease returns the per-Acquire release closure. The closure
// uses an atomic.Bool to make double-release a no-op — defensive
// against caller bugs that would otherwise leak a slot.
func (b *Bulkhead) makeRelease() func() {
	var released atomic.Bool
	return func() {
		if !released.CompareAndSwap(false, true) {
			return
		}
		<-b.sem
	}
}

// noopRelease is the release returned by Acquire on a nil receiver.
// Safe to call any number of times.
func noopRelease() {}
