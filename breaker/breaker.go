package breaker

import (
	"fmt"
	"math"
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

	// consecutiveTrips tracks how many open transitions have
	// happened without a successful close in between. Drives the
	// exponential OpenInterval growth when
	// Config.OpenIntervalMultiplier > 1. Reset on every transition
	// INTO closed.
	consecutiveTrips int

	// currentOpenInterval is the effective open duration for the
	// most-recent trip — computed from base OpenInterval ×
	// Multiplier^(consecutiveTrips-1), capped at OpenIntervalMax.
	// Stored so Allow() does not recompute on every short-circuit.
	currentOpenInterval time.Duration

	// forcedOpenUntil, when non-zero, overrides the
	// currentOpenInterval comparison so ForceOpen(d) holds the
	// breaker open for a caller-supplied window regardless of the
	// adaptive curve. Cleared by ForceClose / any normal
	// transition to closed.
	forcedOpenUntil time.Time
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
		// ForceOpen window: explicit operator override holds the
		// breaker open regardless of the adaptive curve.
		if !b.forcedOpenUntil.IsZero() && now.Before(b.forcedOpenUntil) {
			b.collector.incShortCircuit()
			return false, noopDone
		}
		// Adaptive curve: effective interval = currentOpenInterval
		// (computed at trip time = base × Multiplier^(N-1) capped).
		if now.Sub(b.openedAt) < b.currentOpenInterval {
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

// computeOpenIntervalLocked applies Multiplier^(consecutiveTrips-1)
// to the base OpenInterval, capped at OpenIntervalMax. Caller holds
// b.mu and has already bumped consecutiveTrips.
func (b *Breaker) computeOpenIntervalLocked() time.Duration {
	mult := b.cfg.OpenIntervalMultiplier
	if mult <= 1.0 || b.consecutiveTrips <= 1 {
		return b.cfg.OpenInterval
	}
	exp := math.Pow(mult, float64(b.consecutiveTrips-1))
	d := time.Duration(float64(b.cfg.OpenInterval) * exp)
	if d <= 0 {
		// Overflow on huge consecutive trips — clamp at Max if set,
		// else fall back to base OpenInterval (defensive).
		if b.cfg.OpenIntervalMax > 0 {
			return b.cfg.OpenIntervalMax
		}
		return b.cfg.OpenInterval
	}
	if b.cfg.OpenIntervalMax > 0 && d > b.cfg.OpenIntervalMax {
		return b.cfg.OpenIntervalMax
	}
	return d
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
	if b.halfOpen.succeeded >= b.cfg.HalfOpenSuccessThreshold {
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
		// Successful close clears the adaptive ramp + forced window
		// so the next fresh trip starts at the base OpenInterval.
		b.consecutiveTrips = 0
		b.forcedOpenUntil = time.Time{}
		b.currentOpenInterval = 0
	case StateOpen:
		b.consecutiveTrips++
		b.currentOpenInterval = b.computeOpenIntervalLocked()
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
	b.fireStateChangeLocked(from, to)
}

// fireStateChangeLocked invokes the OnStateChange hook with panic
// recovery. Caller holds b.mu; the hook is allowed to call breaker
// methods because nothing here re-locks. Production code should keep
// the hook short (fire-and-forget alert publish).
func (b *Breaker) fireStateChangeLocked(from, to State) {
	if b.cfg.OnStateChange == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && b.cfg.Logger != nil {
			b.cfg.Logger.Warn("breaker: OnStateChange panic recovered",
				"name", b.cfg.Name,
				"panic", fmt.Sprint(r))
		}
	}()
	b.cfg.OnStateChange(from, to)
}

// ForceOpen jumps the breaker straight to open and holds it there
// for the supplied duration regardless of OpenInterval / adaptive
// curve. Use for operator-driven incident response or maintenance
// windows ("disable this upstream for 30 minutes").
//
// d <= 0 is treated as "use the adaptive OpenInterval normally" —
// effectively just trips the breaker without locking down a window.
// Nil receiver is a no-op.
//
// Transition hooks + metrics fire as usual.
func (b *Breaker) ForceOpen(d time.Duration) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.cfg.Now()
	b.openedAt = now
	if d > 0 {
		b.forcedOpenUntil = now.Add(d)
	}
	b.transitionLocked(StateOpen)
	// If a finite force window was set, override the adaptive
	// currentOpenInterval so the comparison in Allow() uses our
	// duration as the floor.
	if d > 0 && d > b.currentOpenInterval {
		b.currentOpenInterval = d
	}
}

// ForceClose jumps the breaker straight to closed and clears the
// failure window. Use for "manual reset" after an upstream incident
// has been confirmed resolved (so the operator does not wait for
// half-open probes).
//
// Transition hooks + metrics fire as usual. Nil receiver is a no-op.
func (b *Breaker) ForceClose() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.transitionLocked(StateClosed)
}

// Stats is the cheap snapshot returned by [Breaker.Stats] —
// suitable for /admin or /healthz endpoints. One mu acquire.
type Stats struct {
	// State is the current state.
	State State

	// Generation increments on every state transition; useful for
	// detecting flapping (rapid open ↔ closed cycles).
	Generation uint64

	// WindowRequests / WindowFailures are the live totals from the
	// rolling window the closed-state classifier compares against
	// FailureThreshold + MinimumRequests.
	WindowRequests int
	WindowFailures int

	// HalfOpenInFlight / HalfOpenSucceeded mirror the per-cycle
	// probe counters used by the half-open → closed transition.
	HalfOpenInFlight  int
	HalfOpenSucceeded int

	// OpenedAt is the wall time of the most recent trip into open.
	// Zero when the breaker has never tripped.
	OpenedAt time.Time

	// RemainingOpen is the time left until the next Allow can
	// rotate to half-open. Zero when not in open state.
	RemainingOpen time.Duration

	// ConsecutiveTrips counts open transitions without a successful
	// close in between — drives the adaptive OpenInterval growth.
	ConsecutiveTrips int

	// CurrentOpenInterval is the effective open duration applied to
	// the most recent trip (= base × Multiplier^(trips-1) capped).
	// Zero outside open state.
	CurrentOpenInterval time.Duration

	// ForcedOpenUntil is non-zero when ForceOpen(d) has pinned the
	// breaker open through a caller-supplied window.
	ForcedOpenUntil time.Time
}

// Stats returns the cheap point-in-time snapshot of the breaker.
// Nil receiver returns the zero value.
func (b *Breaker) Stats() Stats {
	if b == nil {
		return Stats{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	reqs, fails := b.window.totals()
	s := Stats{
		State:               b.state,
		Generation:          b.gen,
		WindowRequests:      reqs,
		WindowFailures:      fails,
		HalfOpenInFlight:    b.halfOpen.inFlight,
		HalfOpenSucceeded:   b.halfOpen.succeeded,
		OpenedAt:            b.openedAt,
		ConsecutiveTrips:    b.consecutiveTrips,
		CurrentOpenInterval: b.currentOpenInterval,
		ForcedOpenUntil:     b.forcedOpenUntil,
	}
	if b.state == StateOpen {
		now := b.cfg.Now()
		until := b.openedAt.Add(b.currentOpenInterval)
		if !b.forcedOpenUntil.IsZero() && b.forcedOpenUntil.After(until) {
			until = b.forcedOpenUntil
		}
		if remaining := until.Sub(now); remaining > 0 {
			s.RemainingOpen = remaining
		}
	}
	return s
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
