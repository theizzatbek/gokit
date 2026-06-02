package bulkhead

import (
	"sort"
	"sync"
	"time"
)

// Snapshot is the per-tick input handed to a [Controller]. Fields
// outside a controller's interest are ignored — Controller
// implementations declare what they consume.
type Snapshot struct {
	// Capacity is the bulkhead's current MaxConcurrent target.
	Capacity int

	// InFlight is the count of slots currently held by callers.
	InFlight int

	// Waiting is the count of callers parked in cond.Wait.
	Waiting int

	// Latency aggregates wall-clock duration of calls released within
	// the configured window.
	Latency LatencyStats

	// ErrorRate is failures/total over the window. Zero when no
	// observations exist.
	ErrorRate float64

	// SinceLast is the wall time since the previous tick (≈ TickInterval).
	SinceLast time.Duration
}

// LatencyStats are window-aggregated quantiles + count fed into the
// Snapshot. Count is the number of completed calls observed within
// the latency window.
type LatencyStats struct {
	P50, P99 time.Duration
	Count    int
}

// Controller computes the next capacity given a Snapshot. The returned
// value is clamped to [MinCapacity, MaxCapacity] by the bulkhead before
// being applied — controllers do not need to enforce bounds themselves.
type Controller interface {
	Next(s Snapshot) int
}

// AdaptiveConfig configures the auto-tuning controller loop. Required
// when passed via [WithAdaptive].
type AdaptiveConfig struct {
	// Controller decides the next capacity on each tick. Required.
	Controller Controller

	// InitialCap is the starting capacity. Required (>= 1). Clamped
	// to [MinCapacity, MaxCapacity] at construction.
	InitialCap int

	// MinCapacity / MaxCapacity bound the controller's output. Both
	// required; MinCapacity must be >= 1 and <= MaxCapacity.
	MinCapacity int
	MaxCapacity int

	// TickInterval is the cadence of Controller.Next calls. Default
	// 1s when zero. Shorter intervals respond faster but produce
	// noisier capacity curves.
	TickInterval time.Duration

	// WindowSize is how far back the latency window remembers
	// observations. Default 10s. Tick callbacks see observations
	// recorded within this window.
	WindowSize time.Duration
}

// AIMDController implements additive-increase / multiplicative-decrease
// — the simplest stable control law, mirroring TCP congestion control.
//
// Per tick: if ErrorRate >= ErrorThreshold, multiply capacity by
// DecreaseFactor (rounded down, floored at 1). Otherwise add
// IncreaseStep. No-traffic ticks (Latency.Count == 0) leave capacity
// unchanged so an open breaker upstream does not drive the cap to the
// floor.
type AIMDController struct {
	IncreaseStep   int     // default 1
	DecreaseFactor float64 // default 0.5
	ErrorThreshold float64 // default 0.1 (10%)
}

// Next implements [Controller].
func (c *AIMDController) Next(s Snapshot) int {
	if s.Latency.Count == 0 {
		return s.Capacity
	}
	inc := c.IncreaseStep
	if inc <= 0 {
		inc = 1
	}
	dec := c.DecreaseFactor
	if dec <= 0 || dec >= 1 {
		dec = 0.5
	}
	thresh := c.ErrorThreshold
	if thresh <= 0 {
		thresh = 0.1
	}
	if s.ErrorRate >= thresh {
		next := int(float64(s.Capacity) * dec)
		if next < 1 {
			return 1
		}
		return next
	}
	return s.Capacity + inc
}

// adaptiveState holds the runtime data the tick loop reads + writes.
// Constructed inside [New] when [WithAdaptive] is set; nil otherwise.
type adaptiveState struct {
	cfg    AdaptiveConfig
	window *latencyWindow
	stop   chan struct{}
	done   chan struct{}
}

func newAdaptiveState(cfg AdaptiveConfig) *adaptiveState {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = time.Second
	}
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = 10 * time.Second
	}
	return &adaptiveState{
		cfg:    cfg,
		window: newLatencyWindow(cfg.WindowSize),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// loop drives the controller. Exits when stop is closed; signals via
// done so [Bulkhead.Close] can join.
func (a *adaptiveState) loop(b *Bulkhead) {
	defer close(a.done)
	ticker := time.NewTicker(a.cfg.TickInterval)
	defer ticker.Stop()
	last := time.Now()
	for {
		select {
		case <-a.stop:
			return
		case now := <-ticker.C:
			snap := a.snapshot(b, now.Sub(last))
			last = now
			next := a.cfg.Controller.Next(snap)
			if next < a.cfg.MinCapacity {
				next = a.cfg.MinCapacity
			}
			if next > a.cfg.MaxCapacity {
				next = a.cfg.MaxCapacity
			}
			b.SetCapacity(next)
		}
	}
}

// snapshot builds the per-tick Snapshot from the bulkhead's current
// state + the latency window. Reads inflight/waiting/capacity under
// mu briefly.
func (a *adaptiveState) snapshot(b *Bulkhead, since time.Duration) Snapshot {
	stats := a.window.stats()
	b.mu.Lock()
	in := b.inflight
	wait := b.waiting
	cap := b.capacity
	b.mu.Unlock()
	return Snapshot{
		Capacity:  cap,
		InFlight:  in,
		Waiting:   wait,
		Latency:   stats.Lat,
		ErrorRate: stats.Err,
		SinceLast: since,
	}
}

// latencyWindow is a bounded time-windowed ring of {recordedAt,
// duration, success}. Reads evict stale entries lazily on stats().
type latencyWindow struct {
	mu      sync.Mutex
	window  time.Duration
	entries []latencyEntry
}

type latencyEntry struct {
	at      time.Time
	dur     time.Duration
	success bool
}

func newLatencyWindow(window time.Duration) *latencyWindow {
	return &latencyWindow{
		window:  window,
		entries: make([]latencyEntry, 0, 256),
	}
}

func (w *latencyWindow) record(dur time.Duration, success bool) {
	now := time.Now()
	w.mu.Lock()
	w.entries = append(w.entries, latencyEntry{at: now, dur: dur, success: success})
	w.mu.Unlock()
}

type windowStats struct {
	Lat LatencyStats
	Err float64
}

// stats returns aggregated stats over the current window. Stale
// entries are evicted on every call so the slice does not grow
// unboundedly during low-traffic stretches.
func (w *latencyWindow) stats() windowStats {
	cutoff := time.Now().Add(-w.window)
	w.mu.Lock()
	// Drop stale entries from the head.
	idx := 0
	for idx < len(w.entries) && w.entries[idx].at.Before(cutoff) {
		idx++
	}
	if idx > 0 {
		w.entries = w.entries[idx:]
	}
	if len(w.entries) == 0 {
		w.mu.Unlock()
		return windowStats{}
	}
	durs := make([]time.Duration, len(w.entries))
	failures := 0
	for i, e := range w.entries {
		durs[i] = e.dur
		if !e.success {
			failures++
		}
	}
	count := len(w.entries)
	w.mu.Unlock()

	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	p50 := durs[count*50/100]
	p99 := durs[count*99/100]
	if p99 == 0 && count > 0 {
		p99 = durs[count-1]
	}
	return windowStats{
		Lat: LatencyStats{P50: p50, P99: p99, Count: count},
		Err: float64(failures) / float64(count),
	}
}
