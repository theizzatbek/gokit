package email

import (
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

// Option tunes [New].
type Option func(*options)

type options struct {
	logger *slog.Logger
	metric *metrics
}

// WithLogger installs a *slog.Logger that receives Debug on success
// and Warn on send failures. nil silences output.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) { o.logger = l }
}

// WithMetrics registers Prometheus collectors:
//
//   - email_send_total{backend, outcome}
//   - email_send_duration_seconds{backend}
//
// nil reg no-ops.
func WithMetrics(reg prometheus.Registerer) Option {
	return func(o *options) {
		if reg == nil {
			return
		}
		o.metric = newMetrics(reg)
	}
}
