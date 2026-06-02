package bulkhead

import (
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Config configures [New]. Name and MaxConcurrent are required; the
// rest is optional.
type Config struct {
	// Name labels the bulkhead in metrics (the `name` const-label on
	// every bulkhead_* series). Required.
	Name string

	// MaxConcurrent caps the number of in-flight slots. Required
	// (must be > 0): there is no sensible default for "how much
	// concurrency does this upstream tolerate" — it is the bulkhead's
	// whole reason for being.
	MaxConcurrent int

	// MaxQueue caps the number of callers that may wait for a slot
	// when MaxConcurrent is exhausted. The N+MaxQueue+1-th caller
	// gets [ErrBulkheadFull] immediately (fast-fail).
	//
	// 0 = fail-fast (no waiting). Negative values are invalid —
	// unlimited queue is the failure mode bulkhead exists to prevent.
	MaxQueue int

	// QueueTimeout bounds how long Acquire may block in the queue
	// even when the caller's context has a longer deadline. Useful
	// for "fail-fast to a fallback path" patterns where you want to
	// give up on the slow upstream long before the user's request
	// times out.
	//
	// 0 (default) = honour only caller's ctx.
	QueueTimeout time.Duration

	// Logger is reserved for future state-change records (e.g. a
	// saturation alert log). v1 emits nothing; nil = silent.
	Logger *slog.Logger

	// Metrics, when non-nil, registers the kit's standard four
	// bulkhead_* collectors. nil = zero Prometheus footprint.
	Metrics prometheus.Registerer
}

func (c Config) validate() error {
	if c.Name == "" {
		return newError(CodeInvalidName, "bulkhead: Config.Name is required")
	}
	if c.MaxConcurrent < 1 {
		return newError(CodeInvalidMaxConcurrent,
			"bulkhead: Config.MaxConcurrent must be > 0")
	}
	if c.MaxQueue < 0 {
		return newError(CodeInvalidMaxQueue,
			"bulkhead: Config.MaxQueue must be >= 0 (unlimited queue is intentionally unsupported)")
	}
	if c.QueueTimeout < 0 {
		return newError(CodeInvalidQueueTimeout,
			"bulkhead: Config.QueueTimeout must be >= 0")
	}
	return nil
}
