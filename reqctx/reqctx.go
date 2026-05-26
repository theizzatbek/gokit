// Package reqctx carries the per-request identifier (X-Request-ID)
// through context.Context for cross-package propagation. fibermap's
// RequestID middleware writes it; clients/httpc and clients/natsmap
// read it to set outbound headers; service.newLogger wraps slog to
// auto-emit it as an attr on every log line.
package reqctx

import "context"

// HeaderRequestID is the HTTP / NATS header name carrying the request
// identifier. Standard practice across the kit.
const HeaderRequestID = "X-Request-ID"

type requestIDKey struct{}

// WithRequestID returns a copy of ctx carrying id. Empty id values are
// stored as-is; readers (RequestIDFromContext) return "" in that case
// so downstream layers can treat absent and empty uniformly.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDFromContext returns the id stored on ctx, or "" if none.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
