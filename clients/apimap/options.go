package apimap

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/theizzatbek/gokit/clients/httpc"
)

// Option configures Build beyond what the YAML covers.
type Option func(*options)

type options struct {
	logger        *slog.Logger
	metrics       prometheus.Registerer
	baseTransport http.RoundTripper

	// Global httpc-option chain — applied to every built *http.Client.
	// Engine-side per-client overrides (engine.clientHTTPCOptions) are
	// merged AFTER these so client-specific options can refine
	// (e.g. add a per-client middleware on top of a global one).
	httpcOpts []httpc.Option

	// apimap-level hooks. Implemented by injecting an httpc middleware
	// at buildHTTPClient time, scoped to the endpoint so the callback
	// sees the kit-stable (client, endpoint) pair.
	beforeRequest func(client, endpoint string, req *http.Request)
	afterResponse func(client, endpoint string, req *http.Request, resp *http.Response, err error, elapsed time.Duration)

	// Engine-wide default Call merged into every Do/Decode/Exchange
	// before the caller's Call. Caller-side fields win on conflict.
	defaultCall Call
	hasDefault  bool
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

// WithHTTPCOptions passes additional [httpc.Option] values through to
// every *http.Client built by apimap.Engine.Build (one per client +
// one per endpoint with overrides). Use to plug the new httpc features
// — WithMiddleware, WithRetryClassifier, WithBeforeRequest /
// WithAfterResponse, WithProxy / WithTLSConfig, WithIdempotencyKeyHeader
// — uniformly across every upstream.
//
// Per-client overrides via [Engine.RegisterClientOptions] are appended
// AFTER the global slice at Build time, so client-specific options
// refine the global baseline rather than replace it.
//
// Note: WithLogger / WithMetrics / WithBaseTransport remain dedicated
// options at the apimap level because apimap manages those internally
// (the metrics registry is deliberately NOT forwarded to httpc — that
// would re-register the httpc_* collectors and panic on the shared
// registry). Pass httpc-specific knobs through this option instead.
func WithHTTPCOptions(opts ...httpc.Option) Option {
	return func(o *options) { o.httpcOpts = append(o.httpcOpts, opts...) }
}

// WithBeforeRequest fires once per outbound apimap call, BEFORE the
// retry/middleware chain. The callback receives the kit-stable
// (client, endpoint) pair so audit logs can attribute the request
// without re-parsing the URL. Use for: header stamping, tenant
// scoping, span attrs.
//
// Multiple WithBeforeRequest calls — last wins (same semantics as
// httpc.WithBeforeRequest).
func WithBeforeRequest(fn func(client, endpoint string, req *http.Request)) Option {
	return func(o *options) { o.beforeRequest = fn }
}

// WithAfterResponse fires once per outbound apimap call AFTER the
// retry chain returns. Receives the final (response, error) pair
// and the elapsed wall time (including retries). nil response =
// network failure path. Use for: audit logging, custom metrics,
// span attrs.
//
// Multiple WithAfterResponse calls — last wins.
func WithAfterResponse(fn func(client, endpoint string, req *http.Request, resp *http.Response, err error, elapsed time.Duration)) Option {
	return func(o *options) { o.afterResponse = fn }
}

// WithDefaultCall sets an engine-wide Call merged into every
// Do/Decode/Exchange BEFORE the caller's Call. Caller-side fields
// override on conflict.
//
//	apimap.WithDefaultCall(apimap.Call{
//	    Headers: http.Header{"X-Tenant-ID": {"42"}},
//	    Query:   url.Values{"api_version": {"2024-11"}},
//	})
//
// Per-client defaults via [Engine.SetClientDefaultCall] are applied
// AFTER the engine-wide default but BEFORE the caller's Call (i.e.
// client default wins over engine default; caller wins over both).
func WithDefaultCall(c Call) Option {
	return func(o *options) {
		o.defaultCall = c
		o.hasDefault = true
	}
}
