package breaker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Config configures [New]. All durations and counts have sensible
// production defaults applied by applyDefaults; the only required
// field is [Config.Name].
type Config struct {
	// Name labels the breaker in metrics (the `name` label on every
	// breaker_* series). Required.
	Name string

	// FailureThreshold is the number of failures within the rolling
	// window that trips the breaker to open. Default 20.
	FailureThreshold int

	// MinimumRequests is the floor of total requests (failures +
	// successes) that must be observed within the rolling window
	// before the breaker can open. Prevents tripping on a single
	// failure during cold start. Default 10.
	//
	// MinimumRequests is clamped to be >= FailureThreshold at
	// validate time (the floor must not exceed the trip condition;
	// otherwise the breaker can never open).
	MinimumRequests int

	// WindowDuration is the total span of the rolling failure window.
	// Default 10s. The window is divided into WindowSize buckets; old
	// buckets roll out as time advances.
	WindowDuration time.Duration

	// WindowSize is the number of buckets the window is split into.
	// More buckets = smoother roll-off, more memory. Default 10
	// (i.e. one-second buckets when WindowDuration is the default).
	WindowSize int

	// OpenInterval is how long the breaker stays open before moving
	// to half-open. Default 30s. Constant across re-trips by design
	// (adaptive intervals are v2).
	OpenInterval time.Duration

	// HalfOpenMaxProbes caps the number of concurrent probe calls
	// allowed through while in half-open. ALL probes must succeed to
	// transition back to closed; the first failure rotates back to
	// open. Default 1.
	HalfOpenMaxProbes int

	// IsFailure classifies a per-call error as a failure (true) or a
	// success (false). Defaults to:
	//
	//	func(err error) bool {
	//	    if err == nil {
	//	        return false
	//	    }
	//	    if errors.Is(err, context.Canceled) {
	//	        return false
	//	    }
	//	    return true
	//	}
	//
	// context.Canceled is intentionally excluded — a user closing a
	// connection must not charge the upstream's failure budget.
	// context.DeadlineExceeded IS counted as failure (that is the
	// upstream being slow, which is what breakers exist for).
	IsFailure func(error) bool

	// Now is the wall-clock source. Defaults to time.Now. Tests
	// inject a controllable clock.
	Now func() time.Time

	// Logger receives Info entries on every state transition. nil =
	// silent.
	Logger *slog.Logger

	// Metrics, when non-nil, registers the kit's standard four
	// breaker_* collectors. nil = zero Prometheus footprint.
	Metrics prometheus.Registerer
}

// defaultIsFailure is the package's failure classifier when
// Config.IsFailure is nil.
func defaultIsFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	return true
}

func (c Config) validate() error {
	if c.Name == "" {
		return newError(CodeInvalidName, "breaker: Config.Name is required")
	}
	if c.FailureThreshold < 0 {
		return newError(CodeInvalidFailureThreshold,
			"breaker: Config.FailureThreshold must be >= 0 (0 applies default)")
	}
	if c.MinimumRequests < 0 {
		return newError(CodeInvalidMinimumRequests,
			"breaker: Config.MinimumRequests must be >= 0 (0 applies default)")
	}
	// After defaults are conceptually applied, MinimumRequests must
	// be >= FailureThreshold so the breaker can actually trip. We do
	// the comparison on the post-default values to keep the rule
	// honest even when the caller leaves a field at zero.
	ft := c.FailureThreshold
	if ft == 0 {
		ft = defaultFailureThreshold
	}
	mr := c.MinimumRequests
	if mr == 0 {
		mr = defaultMinimumRequests
	}
	if mr < ft {
		return newError(CodeInvalidMinimumRequests,
			"breaker: Config.MinimumRequests must be >= FailureThreshold")
	}
	if c.WindowDuration < 0 {
		return newError(CodeInvalidWindow,
			"breaker: Config.WindowDuration must be >= 0 (0 applies default)")
	}
	if c.WindowSize < 0 {
		return newError(CodeInvalidWindow,
			"breaker: Config.WindowSize must be >= 0 (0 applies default)")
	}
	if c.OpenInterval < 0 {
		return newError(CodeInvalidOpenInterval,
			"breaker: Config.OpenInterval must be >= 0 (0 applies default)")
	}
	if c.HalfOpenMaxProbes < 0 {
		return newError(CodeInvalidHalfOpenMaxProbes,
			"breaker: Config.HalfOpenMaxProbes must be >= 0 (0 applies default)")
	}
	return nil
}

// Defaults for fields left at the zero value.
//
// MinimumRequests must be >= FailureThreshold (otherwise the breaker
// can never trip, because failures cannot exceed total requests).
// The default pair (10, 20) means "trip on 10 failures within at
// least 20 calls" — i.e. ≥50% failure rate.
const (
	defaultFailureThreshold  = 10
	defaultMinimumRequests   = 20
	defaultWindowDuration    = 10 * time.Second
	defaultWindowSize        = 10
	defaultOpenInterval      = 30 * time.Second
	defaultHalfOpenMaxProbes = 1
)

func (c *Config) applyDefaults() {
	if c.FailureThreshold == 0 {
		c.FailureThreshold = defaultFailureThreshold
	}
	if c.MinimumRequests == 0 {
		c.MinimumRequests = defaultMinimumRequests
	}
	if c.WindowDuration == 0 {
		c.WindowDuration = defaultWindowDuration
	}
	if c.WindowSize == 0 {
		c.WindowSize = defaultWindowSize
	}
	if c.OpenInterval == 0 {
		c.OpenInterval = defaultOpenInterval
	}
	if c.HalfOpenMaxProbes == 0 {
		c.HalfOpenMaxProbes = defaultHalfOpenMaxProbes
	}
	if c.IsFailure == nil {
		c.IsFailure = defaultIsFailure
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}
