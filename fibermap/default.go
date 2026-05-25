package fibermap

// Default returns an Engine[T] pre-wired to also enable Prometheus
// [WithMetrics] when [Engine.Run] is called. Since v0.5, the other
// pieces of the ops bundle — Recover, RequestID, RequestLogger, and
// HealthCheck — are built into Run itself and on by default, so
// `New[T]().Run()` already gets them. The only thing Default adds is
// the metrics endpoint, which is opt-in because it pulls in
// `github.com/prometheus/client_golang`.
//
// Use Default when you want the metrics endpoint without spelling it
// out:
//
//	eng := fibermap.Default[AppCtx]()
//	eng.SetContextBuilder(...)
//	eng.RegisterHandler(...)
//	eng.Run(fibermap.WithUse(auth.Bearer()))   // full bundle + auth
//
// Use [New] when you don't want metrics. You can still get the rest of
// the ops bundle:
//
//	eng := fibermap.New[AppCtx]()              // recover + request_id + logger + healthz still on
//	eng.Run(fibermap.WithoutRecover())         // opt out of any default
//
// To override a built-in default — change the health-check path,
// supply a custom slog.Logger, etc. — call the matching With* option:
//
//	eng.Run(fibermap.WithHealthCheck("/_health"))
//	eng.Run(fibermap.WithRecover(myLogger))
func Default[T any]() *Engine[T] {
	e := New[T]()
	e.defaultRunOpts = []RunOption{
		WithMetrics("/metrics"),
	}
	return e
}
