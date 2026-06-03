package natsmap

import (
	"context"
	"time"
)

// wrapWithDispatchHooks layers the apimap-style BeforeDispatch /
// AfterDispatch callbacks (plus natsmap-owned metrics) around a raw
// per-message handler shim. Used by Engine.Build for regular-mode
// subscribers. Batched-mode subscribers go through their own
// wrapping path (per-batch outcomes vs per-message), so this helper
// is regular-mode only.
//
// Order: before → handler → after, with elapsed measured handler-only.
// The metric collectors observe alongside the after-hook so external
// audits and Prometheus see the same outcome classification.
func wrapWithDispatchHooks(
	name, subject string,
	inner func(ctx context.Context, ptr any, meta msgMeta) error,
	before func(name, subject string),
	after func(name, subject string, err error, elapsed time.Duration),
	m *natsmapMetrics,
) func(ctx context.Context, ptr any, meta msgMeta) error {
	if before == nil && after == nil && m == nil {
		return inner
	}
	return func(ctx context.Context, ptr any, meta msgMeta) error {
		if before != nil {
			before(name, subject)
		}
		start := time.Now()
		err := inner(ctx, ptr, meta)
		elapsed := time.Since(start)
		if after != nil {
			after(name, subject, err, elapsed)
		}
		if m != nil {
			outcome := "success"
			if err != nil {
				outcome = "error"
			}
			m.observeHandler(name, outcome, elapsed)
		}
		return err
	}
}
