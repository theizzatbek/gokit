package natsclient

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// headerCarrier adapts a NATS header map (map[string][]string) to OTel's
// propagation.TextMapCarrier interface. OTel's W3C TraceContext propagator
// reads / writes "traceparent" and "tracestate" through this surface so a
// publish-side Inject and a subscribe-side Extract can speak across the
// async NATS boundary.
//
// The propagator only ever reads / writes single-value entries, so the
// adapter coerces multi-value headers to the first value on Get and
// overwrites with a single-value slice on Set. Multi-value headers added
// by the caller (e.g. X-Custom: [a, b]) survive untouched as long as the
// propagator doesn't touch them.
type headerCarrier map[string][]string

// Get returns the first value for key, or "" when absent / empty.
func (c headerCarrier) Get(key string) string {
	if v := c[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// Set overwrites the value list for key with a single-element slice.
func (c headerCarrier) Set(key, value string) { c[key] = []string{value} }

// Keys returns the carrier's key set. Required by the TextMapCarrier
// contract.
func (c headerCarrier) Keys() []string {
	out := make([]string, 0, len(c))
	for k := range c {
		out = append(out, k)
	}
	return out
}

// InjectTraceContext writes the current span's TraceContext into headers
// using the process-global OTel propagator. Idempotent in two senses:
// calling Inject when no propagator is configured (zero-state OTel) is a
// no-op, AND a traceparent already present in headers is preserved
// (NOT overwritten).
//
// The "preserve existing traceparent" behaviour matters for the
// transactional outbox: the originating ctx's span is long-gone by the
// time the Worker's PublishFn dispatches the row, but the original
// TraceContext was snapshotted into Event.Headers at Enqueue time and
// must be the one that lands on the wire — not whatever (usually nil)
// span the Worker happens to run under. Direct publish paths
// (HTTP→NATS) hit the inject branch normally: no traceparent on
// entry, propagator writes one from ctx.
//
// Returns the same map for chainability. Mutates in place when a
// non-nil map is supplied; builds a fresh map otherwise.
func InjectTraceContext(ctx context.Context, headers map[string][]string) map[string][]string {
	if headers == nil {
		headers = map[string][]string{}
	}
	if _, present := headers["traceparent"]; present {
		return headers
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.TextMapCarrier(headerCarrier(headers)))
	return headers
}

// ExtractTraceContext reads W3C TraceContext (traceparent / tracestate)
// from headers and returns a new ctx carrying the remote SpanContext. The
// returned ctx becomes the natural parent for any span the handler opens,
// so the consumer-side trace continues from the publisher's trace.
//
// When no propagator is configured or no trace headers are present, the
// returned ctx is identical to the input.
//
// Used by the kit's subscribe paths (Subscribe, SubscribeRaw) so every
// handler runs with a ctx already aware of the upstream trace.
func ExtractTraceContext(ctx context.Context, headers map[string][]string) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, propagation.TextMapCarrier(headerCarrier(headers)))
}
