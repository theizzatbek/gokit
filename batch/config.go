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
	// non-blocking during the round-trip.
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

	// Logger receives Warn entries on HandlerFn errors. nil =
	// silent (errors still surface via the per-item ack callbacks
	// and the metrics counter).
	Logger *slog.Logger

	// Metrics, when non-nil, registers the kit's standard four
	// batch_* collectors. nil = zero Prometheus footprint.
	Metrics prometheus.Registerer
}
