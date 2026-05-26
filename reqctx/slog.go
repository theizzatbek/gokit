package reqctx

import (
	"context"
	"log/slog"
)

// SlogHandler wraps inner so every Handle call pulls the request_id
// from the Record's context (passed via *Context logging methods) and
// adds it as a `request_id` attr. Idempotent: if inner is already a
// *requestIDHandler, returns it as-is.
//
// Skips injection when:
//   - ctx is nil or carries no request id, or
//   - the Record already has a `request_id` attr (caller-supplied wins).
//
// Usage: wrap once at startup, share the resulting *slog.Logger.
//
//	h := slog.NewJSONHandler(os.Stdout, nil)
//	l := slog.New(reqctx.SlogHandler(h))
//	l.InfoContext(ctx, "request handled")  // emits "request_id":"..."
func SlogHandler(inner slog.Handler) slog.Handler {
	if h, ok := inner.(*requestIDHandler); ok {
		return h
	}
	return &requestIDHandler{inner: inner}
}

type requestIDHandler struct {
	inner slog.Handler
}

func (h *requestIDHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

func (h *requestIDHandler) Handle(ctx context.Context, r slog.Record) error {
	id := RequestIDFromContext(ctx)
	if id == "" {
		return h.inner.Handle(ctx, r)
	}
	// Skip injection if caller already added a `request_id` attr.
	already := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "request_id" {
			already = true
			return false
		}
		return true
	})
	if already {
		return h.inner.Handle(ctx, r)
	}
	cloned := r.Clone()
	cloned.AddAttrs(slog.String("request_id", id))
	return h.inner.Handle(ctx, cloned)
}

func (h *requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &requestIDHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *requestIDHandler) WithGroup(name string) slog.Handler {
	return &requestIDHandler{inner: h.inner.WithGroup(name)}
}
