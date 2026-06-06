# sentrykit

Sentry SDK bootstrap + Fiber middleware + slog → breadcrumb bridge + Crons monitor.

## `sentrykit/`

Sentry error-tracking bootstrap. `sentrykit.Setup(ctx, dsn, opts...) (shutdown, error)` initialises the SDK; `sentrykit.FiberMiddleware()` clones a per-request `*sentry.Hub`, populates HTTP scope (method/route/headers/IP/request_id), propagates the hub via `c.UserContext()` (so `sentry.GetHubFromContext` works in subsystems), and captures panics before re-panicking so `fibermap.Recover` still owns the 500 response. `sentrykit.HubFromContext(c)` returns the request hub (falls back to global); `sentrykit.WrapErrorHandler(inner)` decorates a `fiber.ErrorHandler` to capture 5xx via the request hub.

`sentrykit.SlogHandler(inner, opts...)` wraps any `slog.Handler` so every log record becomes a Sentry breadcrumb on the hub resolved from `ctx`; default level mapping is Debug→skip, Info→info, Warn→warning, Error→error. Opt-in event auto-capture via `sentrykit.WithCaptureLevel(level)` — records ≥ level ship as Sentry events (Exception when an `err`/`error`/`cause` attr holds an `error`, Message otherwise), with `(level, category, message)` dedupe (60s window default).

service-side: `service.WithSentry(dsn, opts...)` wires Setup + middleware + shutdown LIFO so Sentry flushes BEFORE OTel (event-trace_id stays valid) AND auto-wraps the kit-built logger with `SlogHandler` so db/auth/httpc/nats logs feed breadcrumbs into the request hub for free; `WithLogger`-supplied loggers are left untouched. `service.WithSentryBreadcrumbs(...)` forwards handler options; `service.WithSentryErrorCapture(level)` is the one-liner that enables `WithCaptureLevel` on the auto-wrapped logger.

`sentrykit.AutoRelease()` resolves the release tag with no caller wiring (SENTRY_RELEASE env → OTEL_RESOURCE_ATTRIBUTES `service.version=…` → `runtime/debug.ReadBuildInfo` Main.Version → vcs.revision truncated to 12 chars → ""); service.setupSentry prepends it as `WithRelease`, so explicit `sentrykit.WithRelease(...)` from the caller still wins via last-write-wins.

When Auth is also wired, service installs a per-request middleware that tags every Sentry event with `sentry.User{ID: principal.Subject}` (anonymous requests no-op); opt out via `service.WithoutSentryUserScope()`. `auth.SetPrincipalForTest[C]` is the public test helper for injecting a principal into Locals without minting a JWT (production must NOT call it — the suffix is the safety marker).

`sentrykit.MonitorCron(ctx, slug, fn)` / `MonitorCronWithConfig(ctx, slug, cfg, fn)` / `IntervalMonitorConfig(d)` wrap scheduled tasks with Sentry Crons check-ins (`in_progress` → `ok`/`error` with measured duration); the wrappers are transparent pass-throughs when `sentrykit.Setup` hasn't run. service.startRefreshGC auto-wires `MonitorCronWithConfig("kit-refresh-gc", IntervalMonitorConfig(interval), tick)` when both `WithSentry` and `WithRefreshGC` are set; `service.WithSentryRefreshGCSlug(slug)` renames, `service.WithoutSentryRefreshGCMonitor()` disables (useful in multi-replica deployments where one slug per process would inflate alert thresholds).

CPU profiling is intentionally NOT included — `sentry-go` v0.46.2 lacks a stable profiling client option; deferred. `httpc/transport.go` uses `*Context` log variants so its retry/exhausted records resolve the request hub. Native SDK is intentional — Sentry's error fidelity (stack frames, breadcrumbs, contexts) doesn't survive an OTel-events bridge. Performance + metrics stay on `otelkit`. Depends on `github.com/getsentry/sentry-go`.