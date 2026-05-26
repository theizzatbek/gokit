package httpc

import (
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
)

// Option configures the client beyond what Config covers.
type Option func(*options)

type options struct {
	logger              *slog.Logger
	metrics             prometheus.Registerer
	baseTransport       http.RoundTripper
	skipRequestIDHeader bool
}

// WithLogger wires a slog.Logger used for retry-decision and retry-exhaustion
// records. nil = silent (the package default).
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

// WithMetrics registers Prometheus collectors (httpc_requests_total,
// httpc_request_duration_seconds, httpc_retries_total,
// httpc_retries_exhausted_total) on reg. nil = no collectors created.
func WithMetrics(reg prometheus.Registerer) Option {
	return func(o *options) { o.metrics = reg }
}

// WithBaseTransport overrides http.DefaultTransport as the bottom of the
// retry chain. Use this to layer otel-instrumented or auth-injecting
// transports underneath the retry logic.
func WithBaseTransport(rt http.RoundTripper) Option {
	return func(o *options) { o.baseTransport = rt }
}

// WithoutRequestIDHeader opts out of automatically setting
// X-Request-ID on outbound requests. The default behaviour is ON:
// when a request's context carries a request id (via
// reqctx.WithRequestID), the transport sets the X-Request-ID header
// before sending. Explicit per-request headers always win over the
// ctx-derived value.
func WithoutRequestIDHeader() Option {
	return func(o *options) { o.skipRequestIDHeader = true }
}
