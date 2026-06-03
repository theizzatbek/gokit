package lock

import (
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

// Option tunes [New] — wires optional observability.
type Option func(*Lock)

// WithLogger installs a *slog.Logger that records lifecycle events:
//   - Debug on TryAcquire/Acquire success (name, hold start).
//   - Debug on release (name, hold duration).
//   - Warn on acquire / unlock errors (name, err).
//   - Debug on TryAcquire returning false (contended path).
//
// Without this option the lock runs silently.
func WithLogger(l *slog.Logger) Option {
	return func(lk *Lock) { lk.logger = l }
}

// WithMetrics registers Prometheus collectors on reg, tagged with
// the lock's name as a const label:
//
//   - lock_acquires_total{name, outcome=acquired|contended|error} (counter)
//   - lock_hold_duration_seconds{name}                            (histogram)
//
// One lock_* series per Lock name. The kit follows the same
// "one collector per instance" convention as breaker / bulkhead —
// constructing two Lock instances with the same name AND the same
// registry will panic on duplicate registration, so wire each
// distinct lock name to its own [New].
//
// Without this option no collectors are created (zero Prometheus
// footprint).
func WithMetrics(reg prometheus.Registerer) Option {
	return func(lk *Lock) {
		if reg == nil {
			return
		}
		lk.metrics = newMetricsCollector(reg, lk.name)
	}
}
