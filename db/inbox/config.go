package inbox

import (
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

// Config carries the optional observability hooks for an [Inbox]
// handle. Both fields are optional; nil = silent / no collectors.
type Config struct {
	// Logger receives Debug entries on every Process call (outcome,
	// duration, consumer). nil = silent.
	Logger *slog.Logger

	// Metrics, when non-nil, registers the kit's standard
	// inbox_processed_total + inbox_process_duration_seconds vecs.
	// Multiple Inbox handles sharing the same registry coexist — the
	// metric names are package-level, so register only ONCE per
	// process unless you intentionally segment.
	Metrics prometheus.Registerer
}
