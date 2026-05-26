package natsmap

import (
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

// Option configures Build.
type Option func(*options)

type options struct {
	logger  *slog.Logger
	metrics prometheus.Registerer
}

// WithLogger sets the slog.Logger used for natsmap-level events
// (registration warnings, future hot-reload). Per-subscription
// observability is inherited from the natsclient.Client (logger passed
// at Connect).
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

// WithMetrics is the prometheus.Registerer hook. Currently passed
// through for symmetry with apimap; subscription-level counters are
// owned by clients/nats. Reserved for future natsmap-specific
// instrumentation (e.g. registered-publisher / subscriber count gauges).
func WithMetrics(reg prometheus.Registerer) Option {
	return func(o *options) { o.metrics = reg }
}
