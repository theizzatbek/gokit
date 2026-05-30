# sentrykit

Thin Sentry error-tracking bootstrap for kit-based services. One
call initialises the Sentry SDK and returns a flush function;
companion [`FiberMiddleware`](#fibermiddleware) clones a per-request
hub and auto-captures panics; [`WrapErrorHandler`](#wraperrorhandler)
turns 5xx responses into Sentry events.

**Import:** `github.com/theizzatbek/gokit/sentrykit`
**Depends on:** `github.com/getsentry/sentry-go`

## Quickstart

```go
shutdown, err := sentrykit.Setup(ctx, dsn,
    sentrykit.WithEnvironment("production"),
    sentrykit.WithRelease("svc@1.0.0"),
    sentrykit.WithTag("region", "us-east-1"),
)
if err != nil { return err }

defer func() {
    sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    _ = shutdown(sctx)
}()
```

For the typical kit service, `service.WithSentry(dsn, ...)` wraps
Setup + FiberMiddleware + shutdown wiring in one option — see
[service README](../service/README.md).

## Options

| Option | Default | Notes |
|---|---|---|
| `Setup(ctx, dsn)` | — | dsn required; empty value returns an error |
| `WithEnvironment(env)` | "" | `environment` tag on every event (production/staging/dev) |
| `WithRelease(release)` | "" | `release` tag — git SHA, image tag, semver |
| `WithSampleRate(r)` | 1.0 | Fraction of error events shipped (0..1) |
| `WithTracesSampleRate(r)` | 0 | Sentry-native transactions. Keep at 0 when using otelkit for tracing. |
| `WithBeforeSend(fn)` | nil | Hook to scrub PII, drop noisy events, attach extras |
| `WithDebug(bool)` | false | Sentry SDK debug logs to stderr (local setup only) |
| `WithFlushTimeout(d)` | 5s | Default deadline used by shutdown when ctx has none |
| `WithServerName(name)` | auto | Overrides the auto-detected hostname tag |
| `WithTag(key, value)` | — | Pre-populate a tag that lands on every event |

The SDK reads `SENTRY_DSN` and friends from the environment when those
are unset in code — same convention as the rest of the kit.

## FiberMiddleware

```go
app.Use(sentrykit.FiberMiddleware())
```

For each request:

1. Clones `sentry.CurrentHub()` into a request-scoped `*sentry.Hub`.
2. Populates the hub's scope with HTTP context: method, headers, IP,
   request_id (when [`fibermap.RequestID`](../fibermap/README.md) is
   upstream).
3. Stores the hub at `c.Locals(hubKey{})` — readable via
   `sentrykit.HubFromContext(c)`.
4. Defers a `recover()`: on panic, captures the exception with
   request scope and **re-panics**. The outer `fibermap.Recover`
   still writes the 500 response — no behaviour change for the wire.

`http.route` (Fiber's route template, e.g. `/users/:id`) is lazily
attached: Fiber resolves the matched route only after the global
middleware chain advances past `FiberMiddleware`, so the tag is set
either when a handler calls `HubFromContext` or in the deferred panic
path before `RecoverWithContext` runs.

## HubFromContext

```go
sentrykit.HubFromContext(c).CaptureException(err)
sentrykit.HubFromContext(c).Scope().SetUser(sentry.User{ID: subject})
```

Returns the request-scoped hub stored by `FiberMiddleware`. Falls
back to `sentry.CurrentHub()` (process-global) when the middleware
isn't in the chain — callers can always emit, they just lose the
request-scoped tags.

## WrapErrorHandler

```go
app := fiber.New(fiber.Config{
    ErrorHandler: sentrykit.WrapErrorHandler(fibermap.ErrorHandler(logger)),
})
```

Captures the supplied error to the per-request hub when
`errs.HTTP(err)` resolves to status >= 500, then delegates to
`inner`. 4xx errors pass through unchanged — validation failures and
auth rejections are not Sentry-worthy by default.

`service.WithSentry` does NOT auto-wrap because not every service
sets a custom error handler. Wire it explicitly via
`service.WithRunOptions(fibermap.WithFiberConfig(...))`.

## Subsystem breadcrumbs (slog bridge)

`sentrykit.SlogHandler(inner, opts...)` wraps any `slog.Handler` so
every log record passing through it also becomes a Sentry breadcrumb
on the request hub. The inner handler still receives every record —
console / JSON logging keeps working.

```go
inner := slog.NewJSONHandler(os.Stdout, nil)
logger := slog.New(sentrykit.SlogHandler(inner,
    sentrykit.WithAttrFilter(func(k string) bool { return k != "sql" }),
    sentrykit.WithMaxBreadcrumbValueLen(256),
))
```

Hub resolution: `sentry.GetHubFromContext(ctx)` first, then
`sentry.CurrentHub()`. `FiberMiddleware` puts the request hub on
`c.UserContext()` (via `sentry.SetHubOnContext`) so any subsystem
logger that uses `*Context` variants picks up the correct per-request
hub automatically — db's pgx tracer (`LogAttrs(ctx, ...)`), auth's
security logger (`InfoContext(c.UserContext(), ...)`), httpc's retry
log (`WarnContext(req.Context(), ...)`) all qualify out of the box.

Level mapping:

| slog | Sentry breadcrumb level | Default |
|---|---|---|
| Debug | "debug" | **skipped** (use `WithDebugBreadcrumbs`) |
| Info | "info" | on |
| Warn | "warning" | on |
| Error | "error" | on (breadcrumb only — `CaptureException` is opt-in) |

Debug is skipped by default because pgx's query tracer logs every
successful query at Debug level — letting them in would flood the
100-item breadcrumb buffer inside one transactional handler.

Category resolution: explicit attr (default key `category`) → first
word/`:`-prefix of the message lowercased → literal `"log"`. So
`logger.Info("httpc retry", ...)` lands as `category="httpc"`
without code changes.

| Option | Default | Notes |
|---|---|---|
| `WithDebugBreadcrumbs()` | off | Include Debug logs |
| `WithCategoryAttr(key)` | "category" | Slog attr to promote to breadcrumb.Category |
| `WithMaxBreadcrumbValueLen(n)` | 512 | Cap stringified attr values; n ≤ 0 disables |
| `WithAttrFilter(fn)` | nil | Drop attr keys for which fn returns false |
| `WithCaptureLevel(level)` | off | Records ≥ level capture as Sentry events (in addition to breadcrumb) |
| `WithCaptureErrorAttrKeys(keys...)` | err / error / cause | Attr keys consulted for an `error` value → CaptureException; otherwise CaptureMessage |
| `WithCaptureDedupeWindow(d)` | 60s | Suppress duplicate events for same `(level, category, message)` within d; 0 disables |

### Error → event auto-capture

`WithCaptureLevel(slog.LevelError)` turns the handler into a Sentry
sink for high-severity log records:

```go
sentrykit.SlogHandler(inner,
    sentrykit.WithCaptureLevel(slog.LevelError),
)
```

- Records ≥ threshold ship as events. The breadcrumb is added FIRST
  (so the event timeline includes it).
- If an attr in `err` / `error` / `cause` (configurable via
  `WithCaptureErrorAttrKeys`) carries an `error` value, the event is
  a Sentry **Exception** (stack frames + `error.Error()` as
  `Exception.Value`). Otherwise it's a **Message** event with
  `record.Message`.
- Attrs are packed into a single `log` Sentry context block, keeping
  the global tag facet clean.
- Dedupe by fingerprint `(level, category, message)`. The fingerprint
  intentionally ignores attr values — the same `db query failed` shouldn't
  produce a fresh event per concrete query.

`service.WithSentryErrorCapture(slog.LevelError)` is the service-level
shortcut.

`service.WithSentry` auto-wraps the kit-built logger with this
handler. User-supplied loggers (via `service.WithLogger`) are
respected unchanged — opt in manually if you want breadcrumb
coverage there. Use `service.WithSentryBreadcrumbs(...)` to forward
handler options into the auto-wrap path.

## Capture truth table

| Trigger | FiberMiddleware? | WrapErrorHandler? | Captured? |
|---|---|---|---|
| handler panic (error value) | yes | n/a | yes (Exception event) |
| handler panic (string) | yes | n/a | yes (Message event) |
| handler returns `errs.Internal(...)` | yes | yes | yes |
| handler returns `errs.Internal(...)` | yes | no | no (default fiber ErrorHandler has no hook) |
| handler returns `errs.NotFound(...)` | yes | yes | no (status < 500) |
| `HubFromContext(c).CaptureException(err)` explicit | yes | n/a | yes |
| `sentry.CaptureException(err)` package-level (no ctx) | any | n/a | yes (process-global hub, no request scope) |

## Limitations (v1)

- **Traces+metrics out of scope.** Performance belongs to
  [`otelkit`](../otelkit/README.md); Sentry can ingest those via OTLP
  if you point the OTel exporter at Sentry's endpoint.
- **No stack frames from wrapped errors.** When the `err` attr
  carries an `errs.Error` with a `Cause`, the captured Exception
  uses the running goroutine's stack — not the stack the cause was
  produced on. Wiring a `runtime.Frame` extractor onto `errs.Cause`
  is a future follow-up if needed.
- **No release auto-detection.** `WithRelease` must be passed
  explicitly (or `SENTRY_RELEASE` env). Follow-up wires the value
  from `service.Service.NodeName` / `service.version` resource attr.
- **No per-request user scope from JWT.** Handlers can set it
  themselves via `HubFromContext(c).Scope().SetUser(...)` — follow-up
  reads `auth.From(c)` automatically.

## See also

- [`service`](../service/README.md) — `WithSentry(dsn, ...)` wires
  Setup + FiberMiddleware + shutdown in one option
- [`otelkit`](../otelkit/README.md) — performance tracing + metrics
  pipeline; when both are on, Sentry events share the OTel trace_id
- [`errs`](../errs/README.md) — `errs.HTTP(err)` resolves the wire
  status used by `WrapErrorHandler`