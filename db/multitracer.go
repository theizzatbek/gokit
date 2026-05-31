package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// multiTracer fans QueryStart/End out to a fixed ordered slice of
// pgx.QueryTracers. pgx's ConnConfig.Tracer accepts only one tracer
// per connection, so the kit composes its internal logger/metrics
// tracer with any user-supplied tracers (e.g. OTel) here.
//
// Per-tracer context bookkeeping rides under unique slice indices on
// a private key so each child sees its own start-time / state in
// TraceQueryEnd. Children that don't write to the context still work
// — End reads back nil and skips that slot.
type multiTracer struct {
	tracers []pgx.QueryTracer
}

type multiCtxKey struct{}

// TraceQueryStart calls each tracer's TraceQueryStart in order, storing
// the per-tracer context derived by each call in a per-request slice.
// TraceQueryEnd later replays those contexts back to each tracer.
func (m *multiTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	childCtxs := make([]context.Context, len(m.tracers))
	for i, t := range m.tracers {
		childCtxs[i] = t.TraceQueryStart(ctx, conn, data)
	}
	return context.WithValue(ctx, multiCtxKey{}, childCtxs)
}

// TraceQueryEnd dispatches the End event to every child tracer with
// its own previously-returned context. Missing slot (childCtx is nil)
// falls back to the outer ctx so the child still sees a valid context.
func (m *multiTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	childCtxs, _ := ctx.Value(multiCtxKey{}).([]context.Context)
	for i, t := range m.tracers {
		childCtx := ctx
		if i < len(childCtxs) && childCtxs[i] != nil {
			childCtx = childCtxs[i]
		}
		t.TraceQueryEnd(childCtx, conn, data)
	}
}

// composeTracer returns the QueryTracer pgx should install: the kit's
// internal tracer when either logger or metrics is wired, plus every
// extra tracer registered via WithTracer. Returns nil when there is
// nothing to trace (kit has no logger/metrics AND no extras) so pgx
// runs without tracing overhead.
func composeTracer(o *options) pgx.QueryTracer {
	var tracers []pgx.QueryTracer
	if o.logger != nil || o.metrics != nil {
		tracers = append(tracers, &tracer{
			logger:        o.logger,
			metrics:       o.metrics,
			slowThreshold: o.slowThreshold,
		})
	}
	tracers = append(tracers, o.extraTracers...)
	switch len(tracers) {
	case 0:
		return nil
	case 1:
		return tracers[0]
	default:
		return &multiTracer{tracers: tracers}
	}
}
