package batch

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Config configures [New].
type Config[T any] struct {
	// HandlerFn receives every buffered item as one slice per
	// dispatch. Returning nil ack-confirms the whole batch (every
	// item's ack closure fires with nil); returning a non-nil error
	// rolls the batch back (every item's ack fires with that error).
	// Required.
	//
	// HandlerFn runs OUTSIDE the batcher's lock so Submit stays
	// non-blocking during the round-trip. Panics inside HandlerFn
	// are recovered: the panic surfaces as an error to the retry
	// loop and the ack callbacks; the flushLoop survives.
	HandlerFn func(ctx context.Context, batch []T) error

	// BatchSize caps the number of items held in memory before an
	// early flush fires. Required (must be > 0): the size trigger
	// is the primary flush driver, and there is no sensible
	// "unlimited" default for a batched dispatcher.
	BatchSize int

	// Interval bounds the in-memory buffer's age. Defaults to 1s
	// when zero. The flush goroutine treats interval ticks and the
	// size trigger uniformly — whichever fires first calls
	// HandlerFn.
	Interval time.Duration

	// MaxPending caps the in-memory buffer. When > 0 and the buffer
	// is at the cap, Submit drops the item AND calls its ack with
	// ErrPendingFull; TrySubmit returns ErrPendingFull. 0 (default)
	// = unbounded (back-compat — but watch for unbounded growth on
	// slow HandlerFn + fast Submit rates).
	MaxPending int

	// MaxInFlightHandlers caps the number of dispatch goroutines
	// running concurrently. Default 1 (sequential — back-compat).
	// Use > 1 when HandlerFn is slow and Submit rate is high so the
	// pending buffer doesn't accumulate during a long round-trip.
	MaxInFlightHandlers int

	// MaxRetries caps retry attempts on HandlerFn errors (including
	// recovered panics). Default 0 = no retry (current behaviour —
	// the first failure surfaces to acks immediately).
	//
	// Each retry waits RetryBackoffBase × 2^(attempt-1) capped at
	// RetryBackoffMax. Ack fires only after the final attempt.
	MaxRetries int

	// RetryBackoffBase / RetryBackoffMax bound the exponential
	// retry delay. 0 base = no wait between attempts (use with
	// caution — tight loops on a sick upstream).
	RetryBackoffBase time.Duration
	RetryBackoffMax  time.Duration

	// IsRetryable classifies HandlerFn errors as retryable or not.
	// Return false to break the retry loop early and surface the
	// error to the ack callbacks immediately (skipping any unspent
	// retry budget).
	//
	// nil (default) → the kit treats context.Canceled and
	// context.DeadlineExceeded as non-retryable (no point burning
	// the retry budget on a closed ctx) and every other error as
	// retryable. Override when HandlerFn distinguishes transient
	// transport errors from permanent application errors and you
	// want to short-circuit retries on the permanent ones.
	IsRetryable func(err error) bool

	// ContextFn supplies the context used for each dispatch
	// (HandlerFn ctx). When non-nil, it is called once per
	// dispatch — useful for threading a tracer or audit metadata.
	// Caller-supplied Flush(ctx) still wins when ctx != Background.
	// nil (default) = context.Background.
	ContextFn func() context.Context

	// OnBatchStart fires before HandlerFn runs (BEFORE retries).
	// Use for tracing span start, audit-entry begin. Panic-safe.
	OnBatchStart func(ctx context.Context, size int)

	// OnBatchComplete fires after HandlerFn returns (or all retries
	// exhausted). err is the final error from the last attempt
	// (nil on success); elapsed spans the whole retry chain.
	// Panic-safe.
	OnBatchComplete func(ctx context.Context, size int, err error, elapsed time.Duration)

	// Logger receives Warn entries on HandlerFn errors AND on
	// recovered panics from HandlerFn / hooks. nil = silent.
	Logger *slog.Logger

	// Metrics, when non-nil, registers the kit's standard four
	// batch_* collectors. nil = zero Prometheus footprint.
	Metrics prometheus.Registerer
}
