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

	// statsWindow tracks per-acquire wait + per-release call
	// latency observations within Config.StatsWindow. Always-on
	// (adaptive layer has its own window keyed off success outcome;
	// this one is observer-friendly). Read via Stats().
	statsWindow *bulkheadStatsWindow
}

// bulkheadStatsWindow stores recent {recordedAt, latency, isWait}
// observations for Stats() aggregation. Lazy-evict on read.
type bulkheadStatsWindow struct {
	mu      sync.Mutex
	window  time.Duration
	entries []bulkheadStatsEntry
}

type bulkheadStatsEntry struct {
	at      time.Time
	dur     time.Duration
	isWait  bool // true = queue wait sample, false = call latency
}

// Stats is the point-in-time snapshot returned by [Bulkhead.Stats].
// Cheap (one mutex acquire). Suitable for /healthz / admin endpoints.
//
// LatencyP50 / LatencyP99 / AvgWait aggregate observations over
// [Config.StatsWindow] (default 10s). They are zero when no calls
// completed within the window — readers should treat that as "no
// data" rather than "0ns" latency.
type Stats struct {
	InFlight   int
	Waiting    int
	Capacity   int
	LatencyP50 time.Duration
	LatencyP99 time.Duration
	AvgWait    time.Duration
	SampleSize int
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
	winDur := cfg.StatsWindow
	if winDur <= 0 {
		winDur = 10 * time.Second
	}
	b.statsWindow = &bulkheadStatsWindow{window: winDur}
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

// Stats returns the current in-flight / waiting / capacity snapshot
// plus rolling latency + average-wait aggregates over the configured
// [Config.StatsWindow] (default 10s). Nil receiver returns the zero
// value.
func (b *Bulkhead) Stats() Stats {
	if b == nil {
		return Stats{}
	}
	b.mu.Lock()
	in := b.inflight
	wait := b.waiting
	cap := b.capacity
	b.mu.Unlock()
	s := Stats{
		InFlight: in,
		Waiting:  wait,
		Capacity: cap,
	}
	if b.statsWindow != nil {
		p50, p99, avgWait, n := b.statsWindow.aggregate(time.Now())
		s.LatencyP50 = p50
		s.LatencyP99 = p99
		s.AvgWait = avgWait
		s.SampleSize = n
	}
	return s
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
// Fires [Config.OnCapacityChange] on a non-trivial change (prev !=
// next). Nil receiver is a no-op.
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
	if prev != n {
		b.fireCapacityChange(prev, n)
	}
}

// fireCapacityChange invokes the OnCapacityChange hook with panic
// recovery.
func (b *Bulkhead) fireCapacityChange(prev, next int) {
	if b.cfg.OnCapacityChange == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && b.cfg.Logger != nil {
			b.cfg.Logger.Warn("bulkhead: OnCapacityChange panic recovered",
				"name", b.cfg.Name,
				"prev", prev, "next", next)
		}
	}()
	b.cfg.OnCapacityChange(prev, next)
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
		b.recordWait(time.Now(), 0)
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
	waited := time.Since(start)
	b.mu.Unlock()
	b.collector.observe(outcomeOK, waited)
	b.recordWait(time.Now(), waited)
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
	dur := time.Since(start)
	b.mu.Lock()
	b.inflight--
	if b.adaptive != nil {
		b.adaptive.window.record(dur, success)
	}
	b.cond.Signal()
	b.mu.Unlock()
	b.collector.observeCallLatency(dur)
	if b.statsWindow != nil {
		b.statsWindow.record(time.Now(), dur, false)
	}
}

// recordWait registers the queue-wait observation into the always-on
// statsWindow. Called from acquireInternal on every successful
// Acquire (waitDur == 0 for fast-path entries; included so AvgWait
// reflects all callers, not just queued ones).
func (b *Bulkhead) recordWait(now time.Time, waitDur time.Duration) {
	if b == nil || b.statsWindow == nil {
		return
	}
	b.statsWindow.record(now, waitDur, true)
}

// record appends an observation; lazy-evict happens inside
// aggregate. Cap the slice at 4096 entries as defense against
// long-window/high-traffic blowup.
func (w *bulkheadStatsWindow) record(at time.Time, dur time.Duration, isWait bool) {
	w.mu.Lock()
	w.entries = append(w.entries, bulkheadStatsEntry{at: at, dur: dur, isWait: isWait})
	if len(w.entries) > 4096 {
		// Drop the oldest entries beyond the cap.
		w.entries = w.entries[len(w.entries)-4096:]
	}
	w.mu.Unlock()
}

// aggregate computes p50/p99 of call latencies and the average of
// queue-wait observations over the live window. Stale entries are
// evicted on every call.
func (w *bulkheadStatsWindow) aggregate(now time.Time) (p50, p99, avgWait time.Duration, n int) {
	cutoff := now.Add(-w.window)
	w.mu.Lock()
	// Evict.
	idx := 0
	for idx < len(w.entries) && w.entries[idx].at.Before(cutoff) {
		idx++
	}
	if idx > 0 {
		w.entries = w.entries[idx:]
	}
	if len(w.entries) == 0 {
		w.mu.Unlock()
		return 0, 0, 0, 0
	}
	calls := make([]time.Duration, 0, len(w.entries))
	var waitSum time.Duration
	var waitCount int
	for _, e := range w.entries {
		if e.isWait {
			waitSum += e.dur
			waitCount++
		} else {
			calls = append(calls, e.dur)
		}
	}
	n = len(w.entries)
	w.mu.Unlock()

	if len(calls) > 0 {
		// Light O(N log N) sort — N capped at 4096.
		sortDurations(calls)
		p50 = calls[len(calls)*50/100]
		p99Idx := len(calls) * 99 / 100
		if p99Idx >= len(calls) {
			p99Idx = len(calls) - 1
		}
		p99 = calls[p99Idx]
	}
	if waitCount > 0 {
		avgWait = waitSum / time.Duration(waitCount)
	}
	return
}

func sortDurations(s []time.Duration) {
	// Stdlib sort.Slice without importing sort at the top — keeps
	// the package import minimal. n is small (<= 4096).
	insertionSort(s)
}

func insertionSort(s []time.Duration) {
	for i := 1; i < len(s); i++ {
		x := s[i]
		j := i - 1
		for j >= 0 && s[j] > x {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = x
	}
}

// noopRelease is the release returned by Acquire on a nil receiver.
// Safe to call any number of times.
func noopRelease() {}
