package breaker

import (
	"sync"
	"sync/atomic"
	"time"
)

// State is the breaker's current phase. The zero value (StateClosed)
// is the normal pass-through state.
type State int

const (
	// StateClosed — traffic passes through; failures are counted.
	StateClosed State = iota
	// StateOpen — Allow short-circuits every call until OpenInterval
	// elapses, after which the next Allow rotates us to half-open.
	StateOpen
	// StateHalfOpen — at most HalfOpenMaxProbes concurrent probes
	// pass through. All must succeed to return to closed; first
	// failure rotates back to open.
	StateHalfOpen
)

// String returns the lowercase canonical name used in metric labels
// and log fields.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	}
	return "unknown"
}

// Breaker is the three-state circuit breaker. Construct via [New].
// (*Breaker)(nil) is a no-op receiver: Allow always permits, Execute
// just runs fn — this lets callers thread an optional breaker through
// without nil-checks at every call site.
type Breaker struct {
	cfg Config
	mu  sync.Mutex

	state     State
	gen       uint64
	openedAt  time.Time
	window    *window
	halfOpen  halfOpenCounters
	collector *metricsCollector
}

// halfOpenCounters tracks the per-half-open-cycle in-flight and
// success counts. Reset on every transition INTO half-open.
type halfOpenCounters struct {
	inFlight  int
	succeeded int
}

// New validates cfg, applies defaults, and returns a closed Breaker.
// Returns *Error (wrapping a stable Code constant) on invalid config.
func New(cfg Config) (*Breaker, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.applyDefaults()

	b := &Breaker{
		cfg:    cfg,
		state:  StateClosed,
		window: newWindow(cfg.WindowDuration, cfg.WindowSize),
	}
	if cfg.Metrics != nil {
		b.collector = newMetricsCollector(cfg.Metrics, cfg.Name)
		b.collector.setState(StateClosed)
	}
	return b, nil
}

// State returns the breaker's current state.
func (b *Breaker) State() State {
	if b == nil {
		return StateClosed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Allow requests permission to proceed. When allowed it returns
// (true, done); the caller MUST call done exactly once with the
// operation's success outcome. When not allowed it returns
// (false, no-op) — calling the returned closure is safe and a no-op.
//
// The returned done closure is generation-tagged: if the breaker's
// state has rotated by the time the caller invokes it, the outcome
// is dropped. This makes "stale probe answer arrives after re-trip"
// correct.
func (b *Breaker) Allow() (bool, func(success bool)) {
	if b == nil {
		return true, noopDone
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.cfg.Now()
	switch b.state {
	case StateClosed:
		return b.permitClosedLocked()
	case StateOpen:
		if now.Sub(b.openedAt) < b.cfg.OpenInterval {
			b.collector.incShortCircuit()
			return false, noopDone
		}
		b.transitionLocked(StateHalfOpen)
		fallthrough
	case StateHalfOpen:
		if b.halfOpen.inFlight >= b.cfg.HalfOpenMaxProbes {
			b.collector.incShortCircuit()
			return false, noopDone
		}
		b.halfOpen.inFlight++
		gen := b.gen
		var done atomic.Bool
		return true, func(success bool) {
			if !done.CompareAndSwap(false, true) {
				return
			}
			b.finishHalfOpen(gen, success)
		}
	}
	return true, noopDone
}

// permitClosedLocked builds the closed-state done closure. Caller
// holds b.mu.
func (b *Breaker) permitClosedLocked() (bool, func(success bool)) {
	gen := b.gen
	var done atomic.Bool
	return true, func(success bool) {
		if !done.CompareAndSwap(false, true) {
			return
		}
		b.finishClosed(gen, success)
	}
}

// finishClosed records the outcome of a closed-state call and trips
// to open if the threshold is met.
func (b *Breaker) finishClosed(gen uint64, success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if gen != b.gen {
		return
	}
	now := b.cfg.Now()
	b.window.record(now, success)
	b.collector.incOutcome(success)
	if success {
		return
	}
	reqs, fails := b.window.totals()
	if fails >= b.cfg.FailureThreshold && reqs >= b.cfg.MinimumRequests {
		b.openedAt = now
		b.transitionLocked(StateOpen)
	}
}

// finishHalfOpen records the outcome of a probe. Success advances the
// probe counter (all probes succeed → close); failure rotates straight
// back to open with a fresh openedAt.
func (b *Breaker) finishHalfOpen(gen uint64, success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if gen != b.gen {
		return
	}
	now := b.cfg.Now()
	b.halfOpen.inFlight--
	b.collector.incOutcome(success)
	if !success {
		b.openedAt = now
		b.transitionLocked(StateOpen)
		return
	}
	b.halfOpen.succeeded++
	if b.halfOpen.succeeded >= b.cfg.HalfOpenMaxProbes {
		b.transitionLocked(StateClosed)
	}
}

// transitionLocked is the single mutator of b.state. It bumps the
// generation, resets per-state counters, and emits observability
// hooks. Caller holds b.mu.
func (b *Breaker) transitionLocked(to State) {
	from := b.state
	if from == to {
		return
	}
	b.state = to
	b.gen++
	switch to {
	case StateClosed:
		b.window.reset()
		b.halfOpen = halfOpenCounters{}
	case StateHalfOpen:
		b.halfOpen = halfOpenCounters{}
	}
	b.collector.setState(to)
	b.collector.recordTransition(from, to)
	if b.cfg.Logger != nil {
		b.cfg.Logger.Info("breaker state transition",
			"name", b.cfg.Name,
			"from", from.String(),
			"to", to.String())
	}
}

// Execute is the ergonomic wrapper: Allow + run + done. The error
// returned by fn is classified via Config.IsFailure; the outcome is
// fed to done. Returns ErrOpen if the breaker did not allow the call.
//
// Execute does NOT recover panics — if fn panics, done is NOT called
// (the surrounding defer chain runs as usual). Callers wanting "panic
// is a failure" semantics wrap fn themselves.
func (b *Breaker) Execute(fn func() error) error {
	allowed, done := b.Allow()
	if !allowed {
		return ErrOpen
	}
	err := fn()
	if b == nil {
		done(true)
		return err
	}
	done(!b.cfg.IsFailure(err))
	return err
}

// noopDone is the done callback returned when Allow short-circuits or
// when the receiver is nil. Safe to call any number of times.
func noopDone(bool) {}
