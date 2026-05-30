package service

import (
	"context"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
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
	// Prepend an auto-detected release so an explicit
	// sentrykit.WithRelease passed by the caller still wins via the
	// last-write-wins functional-options pipeline.
	allOpts := s.opts.sentryOpts
	if rel := sentrykit.AutoRelease(); rel != "" {
		allOpts = append([]sentrykit.Option{sentrykit.WithRelease(rel)}, allOpts...)
	}
	shutdown, err := sentrykit.Setup(ctx, s.opts.sentryDSN, allOpts...)
	if err != nil {
		return err
	}
	s.sentryShutdown = shutdown

	s.opts.fiberMiddleware = append(
		s.opts.fiberMiddleware,
		sentrykit.FiberMiddleware(),
	)
	// Per-request user scope: when Auth is wired AND the caller did
	// not opt out, append a middleware that reads auth.From[C](c)
	// and tags the hub with the authenticated subject. Runs AFTER
	// sentrykit.FiberMiddleware (so the hub clone exists on ctx)
	// and AFTER auth.Bearer (which fills the principal Locals slot
	// — Bearer is prepended at runOptions time before
	// opts.fiberMiddleware).
	if s.Auth != nil && !s.opts.skipSentryUserScope {
		s.opts.fiberMiddleware = append(
			s.opts.fiberMiddleware,
			s.sentryUserScopeMiddleware(),
		)
	}
	return nil
}

// sentryUserScopeMiddleware tags every Sentry event captured during
// this request with sentry.User{ID: principal.Subject}. Anonymous
// requests (auth.From returns no principal) no-op so events ship
// with an empty User — which Sentry handles fine.
//
// Only Subject is exposed by default to keep PII surface predictable:
// custom claims may carry email/name but the kit doesn't know `C`'s
// shape. Handlers wanting richer User can call
// sentrykit.HubFromContext(c).Scope().SetUser(sentry.User{...}) — the
// later call wins because scope mutations are last-write-wins within
// the request hub.
func (s *Service[T, C]) sentryUserScopeMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if p, ok := auth.From[C](c); ok && p != nil && p.Subject != "" {
			sentrykit.HubFromContext(c).Scope().SetUser(sentry.User{ID: p.Subject})
		}
		return c.Next()
	}
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
