package ratelimit

import (
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

// Option tunes [NewRedis].
type Option func(*Redis)

// WithLogger installs a slog.Logger that receives Debug entries on
// allowed checks and Warn entries on Redis transport errors. nil
// silences output entirely.
func WithLogger(l *slog.Logger) Option {
	return func(r *Redis) { r.logger = l }
}

// WithMetrics registers Prometheus collectors on reg:
//
//   - ratelimit_requests_total{outcome=allowed|denied}
//   - ratelimit_allow_duration_seconds (histogram)
//   - ratelimit_backend_errors_total
//
// Passing nil reg is a no-op (no collectors registered).
func WithMetrics(reg prometheus.Registerer) Option {
	return func(r *Redis) {
		if reg == nil {
			return
		}
		r.metric = newMetrics(reg)
	}
}
