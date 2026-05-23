package fibermap

import "log/slog"

// Default returns an Engine[T] pre-wired with sensible production
// defaults that [Engine.Run] applies automatically:
//
//   - [Recover] with slog.Default() — catch panics, log with stack, 500
//   - [RequestID]() at the front of the Use chain — every request
//     carries an X-Request-ID
//   - [RequestLogger] with slog.Default(), skipping `/healthz` and `/metrics`
//   - [WithHealthCheck]("/healthz") — k8s livenessProbe endpoint
//   - [WithMetrics]("/metrics") — Prometheus scrape endpoint
//
// Call SetContextBuilder and Register* like usual, then Run():
//
//	eng := fibermap.Default[AppCtx]()
//	eng.SetContextBuilder(...)
//	eng.RegisterHandler(...)
//	eng.RegisterMiddlewareFactory(...)
//	eng.Run()                       // ops bundle is on
//
// To override a default, pass the same option to Run with new
// arguments — later options win:
//
//	eng.Run(fibermap.WithHealthCheck("/_health"))           // path override
//	eng.Run(fibermap.WithMetrics(""))                       // disable metrics
//	eng.Run(fibermap.WithRecover(myLogger))                 // structured logger
//
// To add MORE Fiber-level middleware on top of the default RequestID:
//
//	eng.Run(fibermap.WithUse(auth.Bearer()))                // request_id + auth
//
// Use [New] instead of Default when you want zero defaults — handy
// for tests, embedded use, or unusual deployments.
func Default[T any]() *Engine[T] {
	e := New[T]()
	logger := slog.Default()
	e.defaultRunOpts = []RunOption{
		WithRecover(logger),
		WithRequestLogger(logger, "/healthz", "/metrics"),
		WithHealthCheck("/healthz"),
		WithMetrics("/metrics"),
		WithUse(RequestID()),
	}
	return e
}
