package service

import (
	"context"
	"time"

	"github.com/theizzatbek/gokit/sentrykit"
)

// setupSentry runs sentrykit.Setup when WithSentry was passed and
// appends sentrykit.FiberMiddleware to opts.fiberMiddleware. Order
// of integration:
//
//   - setupOtel runs first and prepends otelfiber. The trace span
//     therefore opens BEFORE the sentry hub clone, so any Sentry
//     event captured in this request carries the otel trace_id on
//     the scope's Contexts (sentry-go reads it from
//     trace.SpanContextFromContext when Setup wires the propagator).
//   - Sentry middleware is APPENDED (not prepended) so it sits
//     INSIDE otelfiber but still OUTSIDE every user middleware —
//     panics in CORS/auth/custom middleware reach the sentry recover
//     before fibermap.Recover writes the 500.
//
// Errors from sentrykit.Setup propagate; the caller's Close path
// tears down whatever subsystems were already built.
func (s *Service[T, C]) setupSentry(ctx context.Context) error {
	if s.opts == nil || s.opts.sentryDSN == "" {
		return nil
	}
	shutdown, err := sentrykit.Setup(ctx, s.opts.sentryDSN, s.opts.sentryOpts...)
	if err != nil {
		return err
	}
	s.sentryShutdown = shutdown

	s.opts.fiberMiddleware = append(
		s.opts.fiberMiddleware,
		sentrykit.FiberMiddleware(),
	)
	return nil
}

// registerSentryShutdown registers the sentry flush callback with
// OnShutdown. The kit's OnShutdown is LIFO; registering sentry AFTER
// the otel callback means sentry flushes FIRST during Close, so any
// last-second events that reference an otel trace_id land before the
// otel span exporter shuts down.
func (s *Service[T, C]) registerSentryShutdown() {
	if s.sentryShutdown == nil {
		return
	}
	shutdown := s.sentryShutdown
	s.OnShutdown(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return shutdown(ctx)
	})
}