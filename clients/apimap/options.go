package apimap

import (
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
)

// Option configures Build beyond what the YAML covers.
type Option func(*options)

type options struct {
	logger        *slog.Logger
	metrics       prometheus.Registerer
	baseTransport http.RoundTripper
}

// WithLogger is passed through to clients/httpc.WithLogger for every
// underlying *http.Client built at Engine.Build time. nil = silent.
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

// WithMetrics is passed through to clients/httpc.WithMetrics. nil = no
// collectors registered.
func WithMetrics(reg prometheus.Registerer) Option {
	return func(o *options) { o.metrics = reg }
}

// WithBaseTransport is passed through to clients/httpc.WithBaseTransport.
// Use this to layer otel-instrumented or auth-injecting transports under
// the retry chain.
func WithBaseTransport(rt http.RoundTripper) Option {
	return func(o *options) { o.baseTransport = rt }
}
