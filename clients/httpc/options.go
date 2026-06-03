package httpc

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/theizzatbek/gokit/breaker"
	"github.com/theizzatbek/gokit/bulkhead"
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
	bulkhead            *bulkhead.Bulkhead

	retryClassifier     func(*http.Request, *http.Response, error) bool
	retryStatusCodes    map[int]struct{}
	retryNonIdempotent  bool
	idempotencyKeyHdr   string
	middleware          []func(http.RoundTripper) http.RoundTripper
	beforeRequest       func(*http.Request)
	afterResponse       func(*http.Request, *http.Response, error, time.Duration)
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

// WithBulkhead installs a concurrency-cap bulkhead between the retry
// layer and the circuit breaker. Each retry attempt independently
// Acquires a slot; release fires as soon as base.RoundTrip returns
// (BEFORE the caller reads the body — slot tracks the network round-
// trip, not the body lifetime).
//
// Rejection surfaces as *errs.Error: [CodeBulkheadFull]
// (KindUnavailable, wraps [bulkhead.ErrBulkheadFull]) when the
// MaxConcurrent+MaxQueue cap is reached, or [CodeBulkheadQueueTimeout]
// (KindTimeout, wraps [bulkhead.ErrQueueTimeout]) when the configured
// QueueTimeout fires. The retry layer recognises ErrBulkheadFull as
// non-retryable; ErrQueueTimeout passes through as a normal transient.
//
// nil disables the bulkhead (the default). Shared across multiple
// *http.Client instances via repeated WithBulkhead(b) — apimap uses
// this to share one bulkhead between endpoint-override clients of
// the same upstream.
func WithBulkhead(b *bulkhead.Bulkhead) Option {
	return func(o *options) { o.bulkhead = b }
}

// WithRetryClassifier overrides the kit's default retry-decision
// rule. Return true to retry the (request, response, err) tuple,
// false to surface it to the caller. The classifier wins over
// WithRetryStatusCodes — pass nil to fall back to the default
// (idempotent method + transient status set).
//
// The default rule still applies when this option is not set:
//
//   - Method MUST be idempotent (POST/PATCH never retry unless
//     WithRetryOnNonIdempotent is on or the request carries the
//     configured Idempotency-Key header).
//   - Response status MUST be 408 / 429 / 5xx, OR err MUST be a
//     network failure.
//
// Use to add app-specific retryable conditions (423 Locked for
// pessimistic-lock workflows, custom 5xx variants) or to suppress a
// noisy upstream (e.g. don't retry 429 because the caller handles
// rate-limit replay itself).
func WithRetryClassifier(fn func(req *http.Request, resp *http.Response, err error) bool) Option {
	return func(o *options) { o.retryClassifier = fn }
}

// WithRetryStatusCodes overrides just the transient-status set.
// Passing no codes disables status-based retries entirely (only
// network errors still trigger retries when the default classifier
// runs). Has no effect when WithRetryClassifier is set — the custom
// classifier owns the full decision.
//
//	WithRetryStatusCodes(503, 504)             // only timeout-ish
//	WithRetryStatusCodes(408, 429, 500, 502, 503, 504) // explicit default
func WithRetryStatusCodes(codes ...int) Option {
	return func(o *options) {
		set := make(map[int]struct{}, len(codes))
		for _, c := range codes {
			set[c] = struct{}{}
		}
		o.retryStatusCodes = set
	}
}

// WithRetryOnNonIdempotent permits retrying POST and PATCH. By
// default the kit refuses because a retry can cause a double-write
// (charge twice, send twice, etc.). Enable only when the caller has
// confirmed every POST is idempotent — typically because the upstream
// API supports an idempotency key. Prefer WithIdempotencyKeyHeader
// over this flag when possible.
func WithRetryOnNonIdempotent(on bool) Option {
	return func(o *options) { o.retryNonIdempotent = on }
}

// WithIdempotencyKeyHeader enables retry on POST/PATCH ONLY when the
// outbound request carries the named header. Use to opt into
// Stripe-style "safe POST retry": the caller sets `Idempotency-Key:
// <uuid>` on the request, the upstream guarantees the call is
// idempotent, the kit retries on transient failure.
//
//	WithIdempotencyKeyHeader("Idempotency-Key")
//
// Empty (default) = no retry on non-idempotent methods regardless of
// header. WithRetryOnNonIdempotent(true) wins if both are set.
func WithIdempotencyKeyHeader(name string) Option {
	return func(o *options) { o.idempotencyKeyHdr = name }
}

// WithMiddleware appends RoundTripper decorators layered ABOVE the
// kit's retry + metrics chain but BELOW the X-Request-ID auto-stamp.
// Multiple calls accumulate; the slice is applied in reverse so the
// FIRST middleware sees the request FIRST (outermost decorator).
//
//	WithMiddleware(
//	    func(next http.RoundTripper) http.RoundTripper { return authMW{next} },
//	    func(next http.RoundTripper) http.RoundTripper { return auditMW{next} },
//	)
//	// Chain: requestID → auth → audit → retry → … → base
//
// Decorators see every retry attempt as a separate RoundTrip call
// (because retry lives below them) — be aware when injecting
// auth-refresh logic that depends on "first request only".
func WithMiddleware(mw ...func(http.RoundTripper) http.RoundTripper) Option {
	return func(o *options) { o.middleware = append(o.middleware, mw...) }
}

// WithBeforeRequest fires once per outbound RoundTrip, BEFORE the
// retry loop and any user middleware. Use for cheap mutation (header
// stamping, scope-aware audit) where a full RoundTripper decorator
// would be overkill. Multiple WithBeforeRequest calls — only the last
// wins.
func WithBeforeRequest(fn func(req *http.Request)) Option {
	return func(o *options) { o.beforeRequest = fn }
}

// WithAfterResponse fires once per outbound RoundTrip, AFTER the
// retry loop returns. Receives the final (response, error) pair and
// the elapsed wall time (including retries). nil response = network
// failure; non-nil response = whatever the kit returned to the
// caller, including drained-body exhausted-retry responses.
//
// Multiple WithAfterResponse calls — only the last wins.
func WithAfterResponse(fn func(req *http.Request, resp *http.Response, err error, elapsed time.Duration)) Option {
	return func(o *options) { o.afterResponse = fn }
}

// WithProxy plugs a proxy URL into a fresh *http.Transport at the
// bottom of the chain. Equivalent to:
//
//	WithBaseTransport(&http.Transport{Proxy: http.ProxyURL(u)})
//
// but composable with WithDialer / WithTLSConfig — the kit
// synthesises one shared *http.Transport when any of the three are
// set. Explicit WithBaseTransport wins over all three.
func WithProxy(u *url.URL) Option {
	return func(o *options) {
		tr := ensureSharedTransport(o)
		tr.Proxy = http.ProxyURL(u)
	}
}

// WithDialer overrides the default DialContext used by the kit's
// shared *http.Transport. Composable with WithProxy / WithTLSConfig.
func WithDialer(dial func(ctx context.Context, network, addr string) (net.Conn, error)) Option {
	return func(o *options) {
		tr := ensureSharedTransport(o)
		tr.DialContext = dial
	}
}

// WithTLSConfig sets TLSClientConfig on the kit's shared
// *http.Transport. Composable with WithProxy / WithDialer.
//
// Note: this is the TLS config the kit's HTTP client uses, NOT the
// NATS-side TLS — those live in clients/nats. For self-signed
// upstreams build a *tls.Config with the trust pool and pass it
// here.
func WithTLSConfig(cfg *tls.Config) Option {
	return func(o *options) {
		tr := ensureSharedTransport(o)
		tr.TLSClientConfig = cfg
	}
}

// ensureSharedTransport returns the *http.Transport o.baseTransport
// already points at, or builds and stores a fresh one cloned from
// http.DefaultTransport. Used by WithProxy / WithDialer /
// WithTLSConfig so the three options compose into one Transport
// instead of each replacing the previous.
//
// Explicit WithBaseTransport with a non-*http.Transport (e.g. otel
// wrapper) wins — the helpers no-op silently in that case.
func ensureSharedTransport(o *options) *http.Transport {
	if tr, ok := o.baseTransport.(*http.Transport); ok {
		return tr
	}
	if o.baseTransport != nil {
		// Caller passed a non-*http.Transport (e.g. otelhttp). Don't
		// mutate it — return a throwaway *http.Transport. The shortcut
		// options will populate fields no one reads. We could
		// alternatively panic but silently no-op is friendlier.
		return &http.Transport{}
	}
	base, _ := http.DefaultTransport.(*http.Transport)
	if base == nil {
		base = &http.Transport{}
	}
	tr := base.Clone()
	o.baseTransport = tr
	return tr
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
