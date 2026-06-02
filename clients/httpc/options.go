package httpc

import (
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/theizzatbek/gokit/breaker"
)

// Option configures the client beyond what Config covers.
type Option func(*options)

type options struct {
	logger              *slog.Logger
	metrics             prometheus.Registerer
	baseTransport       http.RoundTripper
	skipRequestIDHeader bool
	breaker             *breaker.Breaker
	breakerFailureFn    func(*http.Response, error) bool
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

// WithBreaker installs a circuit breaker between the retry layer and
// the base transport. Each attempt consults the breaker; an open
// breaker returns a *errs.Error with [CodeCircuitOpen] (wrapping
// [breaker.ErrOpen]) which the retry layer recognises as
// non-retryable and propagates immediately.
//
// nil disables the breaker (the default). The breaker's lifetime is
// owned by the caller; a single *breaker.Breaker may be shared
// across multiple *http.Client instances pointing at the same
// upstream (this is how apimap shares one breaker between endpoints
// with httpc overrides).
func WithBreaker(b *breaker.Breaker) Option {
	return func(o *options) { o.breaker = b }
}

// WithBreakerFailureClassifier overrides the default rule for what
// counts as a failure for breaker bookkeeping. Return true to mark
// the (resp, err) pair as a failure.
//
// Default: error != nil (except context.Canceled), or response with
// status in {408, 429, 500, 502, 503, 504}. All other responses
// (including non-2xx 4xx) are successes — they reflect client error,
// not upstream health.
//
// No-op when WithBreaker was not set.
func WithBreakerFailureClassifier(fn func(*http.Response, error) bool) Option {
	return func(o *options) { o.breakerFailureFn = fn }
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
