# service

All-in-one service helper. One `service.New(ctx, cfg)` builds the bundled runtime — `*db.DB`, `*auth.Auth[C]`, `*natsclient.Client`, `*natsmap.Runtime`, `*http.Client`, `*apimap.Client`, `*fibermap.Engine[T]` — with auto-detect optionality (subsystems with empty config stay nil). Auto-installs `auth.Bearer(BearerOptional)` at fiber.App level via `WithUse` so `ContextBuilder` reads JWT subject correctly (fixes a real gotcha) and wires the `bearer:` middleware factory onto the engine; `/auth/login` `/auth/refresh` `/auth/logout` are NOT auto-mounted — declare your own login handler and call `svc.Auth.IssueLogin / IssueRefresh / Logout`. `Run()` blocks with the production-ops bundle. Service is additive over the existing subpackages — go straight to `svc.DB.Tx(...)` / `svc.Auth.Sign(...)` for anything Service doesn't shortcut.

**Import:** `github.com/theizzatbek/gokit/service`
**Depends on:** every other `gokit/*` subpackage

## Why use it

Wiring a kit-based service hand-rolls ~200 lines: `KeySet` from PEM, `auth.New` + `refreshpg.New` plumbing, `httpc.New`, `apimap.New + LoadFile + Build` (with the `${MICROLINK_BASE_URL}` env trick), `natsclient.Connect`, `fibermap.Default + SetValidator`, `fibermount.MountMiddlewareFactories`, install `Bearer(BearerOptional)` at fiber.App level via `WithUse` (or quietly hit the "AppCtx.UserID is empty in handlers" trap), assemble `RunOption`s, manage graceful shutdown, set up `slog`. `service` is that bundle. Your service still registers its own auth handlers (login body shape, credential check, custom auth schemes) — typically a few lines that delegate to `svc.Auth.IssueLogin` / `IssueRefresh` / `Logout`.

The `examples/urlshort` `main.go` shrinks from ~270 → ~80 lines after switching to Service.

## Quickstart

```go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/caarlos0/env/v11"
    "github.com/gofiber/fiber/v2"

    "github.com/theizzatbek/gokit/auth"
    "github.com/theizzatbek/gokit/fibermap"
    "github.com/theizzatbek/gokit/service"
)

type AppCtx struct{ UserID string }
type Claims struct {
    Email string `json:"email"`
}

func main() {
    var cfg service.Config
    if err := env.Parse(&cfg); err != nil { log.Fatal(err) }

    ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

    svc, err := service.New[AppCtx, Claims](ctx, cfg)
    if err != nil { log.Fatal(err) }
    defer svc.Close()

    svc.SetContextBuilder(func(c *fiber.Ctx) (AppCtx, error) {
        return AppCtx{UserID: svc.Auth.Subject(c)}, nil
    })

    // Custom login handler — service owns body shape and verification.
    type LoginRequest struct {
        Login    string `json:"login"    validate:"required"`
        Password string `json:"password" validate:"required,min=1"`
    }
    fibermap.RegisterHandlerWithBody(svc.Engine, "auth.login",
        func(c *fibermap.Context[AppCtx], body LoginRequest) error {
            // look up user, check password ...
            return svc.Auth.IssueLogin(c.Ctx, auth.LoginResult[Claims]{
                Subject: "uid",
                Custom:  Claims{Email: body.Login},
            })
        })
    fibermap.RegisterHandler(svc.Engine, "auth.refresh",
        func(c *fibermap.Context[AppCtx]) error { return svc.Auth.IssueRefresh(c.Ctx) })
    fibermap.RegisterHandler(svc.Engine, "auth.logout",
        func(c *fibermap.Context[AppCtx]) error { return svc.Auth.Logout(c.Ctx) })

    fibermap.RegisterHandler(svc.Engine, "ping", func(c *fibermap.Context[AppCtx]) error {
        return c.SendString("pong")
    })

    if err := svc.Run(); err != nil { log.Fatal(err) }
}
```

## Configuration

Env-driven via `caarlos0/env/v11`. Compose into your own app config via embedding to add app-specific fields.

### K8s boot resilience

Service initializes DB and NATS with bounded retry (5 attempts,
1s→16s exponential backoff) by default — accommodates the common
pattern where postgres/nats containers Ready a few seconds after
the service container starts. Opt out via `WithoutConnectRetry()`
or set the per-subsystem env sentinel `_CONNECT_MAX_RETRIES=-1`.

### Top-level `service.Config`

| Section | Prefix | Trigger | Notes |
|---|---|---|---|
| `Service` | (none) | always | `ADDR`, `LOG_LEVEL`, `LOG_FORMAT` |
| `DB` | `DB_` | `DB_USER` set | When omitted, `svc.DB == nil` |
| `Auth` | `AUTH_` | `AUTH_PRIVATE_KEY_PEM` set | Requires DB (refreshpg store) |
| `NATS` | `NATS_` | `NATS_URL` set | Independent |
| `NATSMap` | `NATSMAP_` | `NATSMAP_ENABLED=true` or path set | Requires NATS |
| `HTTPC` | `HTTPC_` | always | Zero-value → sensible defaults |
| `APIMap` | `APIMAP_` | `APIMAP_ENABLED=true` or `APIMAP_PATH` set | Clients YAML |
| `Routes` | `ROUTES_` | `ROUTES_ENABLED=true` or `ROUTES_PATH` set | Routes YAML |

### `ServiceConfig`

| Field | Env | Default |
|---|---|---|
| `Addr` | `ADDR` | `:3000` |
| `LogLevel` | `LOG_LEVEL` | `info` |
| `LogFormat` | `LOG_FORMAT` | `json` (also: `text`) |
| `NodeName` | `SERVICE_NODE_NAME` | `os.Hostname()` if unset. Flows to `natsclient.Config.Name` (when `NATS.Name` is not explicit) and to default slog attrs (`node=...`). |
| `ServerGroup` | `SERVICE_SERVER_GROUP` | Empty by default. When set, passed to `natsmap.WithServerGroup(...)` — auto-derived subscriber queue groups suffix with `-<ServerGroup>` for cross-region isolation. See [natsmap multi-node](../clients/natsmap/README.md#multi-node-behaviour). |

### `DBConfig`

Full field list lives in [db/README](../db/README.md#configuration). The
multi-node-relevant env vars surfaced through service:

| Field | Env | Notes |
|---|---|---|
| `URL` | `DB_URL` | full postgres connection string (overrides `DB_HOST`/`DB_PORT`/…). Supports comma-separated multi-host URLs for primary failover. |
| `AppName` | `DB_APP_NAME` | `application_name` sent to Postgres; auto-set from `SERVICE_NODE_NAME` when empty. |
| `HasReadReplica` | `DB_HAS_READ_REPLICA` | opt into the standby pool; `svc.DB.ReadQuery(...)` then targets a standby. Requires PG 14+. |

### `AuthConfig`

| Field | Env | Default |
|---|---|---|
| `PrivateKeyPEM` | `AUTH_PRIVATE_KEY_PEM` | (opt-in trigger) |
| `KID` | `AUTH_KID` | `k1` |
| `Issuer` | `AUTH_ISSUER` | `gokit` |
| `AccessTTL` | `AUTH_ACCESS_TTL` | `15m` |
| `RefreshTTL` | `AUTH_REFRESH_TTL` | `720h` (30 days) |

### `NATSConfig`

| Field | Env |
|---|---|
| `URL` | `NATS_URL` |
| `Name` | `NATS_NAME` |

### `APIMapConfig`

| Field | Env |
|---|---|
| `Enabled` | `APIMAP_ENABLED` |
| `Path` | `APIMAP_PATH` |

### `NATSMapConfig`

| Field | Env |
|---|---|
| `Enabled` | `NATSMAP_ENABLED` |
| `SubscribersPath` | `NATSMAP_SUBSCRIBERS_PATH` |
| `PublishersPath` | `NATSMAP_PUBLISHERS_PATH` |

Either path (or `Enabled=true`) triggers auto-build via `clients/natsmap`. Both paths may point at the same combined YAML. Requires `NATS` to be configured (`service_natsmap_needs_nats` otherwise).

### `RoutesConfig`

| Field | Env |
|---|---|
| `Enabled` | `ROUTES_ENABLED` |
| `Path` | `ROUTES_PATH` |

When `Enabled=true` or `Path` is set, routes YAML is loaded and mounted at `svc.Run()` time. If `Path` is empty and `Enabled=true`, uses `service.DefaultRoutesPath` (`routes.yaml`).

## Default paths convention

Each YAML-driven subsystem exposes an `Enabled` flag plus an optional `Path` override. When `Enabled=true` and no `Path` is set, service uses the canonical default filename — drop the file in your binary's working directory and you're done.

| Subsystem | Enabled env | Default filename | Path override env |
|---|---|---|---|
| apimap | `APIMAP_ENABLED` | `service.DefaultAPIMapPath` (`clients.yaml`) | `APIMAP_PATH` |
| natsmap subscribers | `NATSMAP_ENABLED` | `service.DefaultNATSMapSubscribersPath` (`subscribers.yaml`) | `NATSMAP_SUBSCRIBERS_PATH` |
| natsmap publishers | `NATSMAP_ENABLED` | `service.DefaultNATSMapPublishersPath` (`publishers.yaml`) | `NATSMAP_PUBLISHERS_PATH` |
| routes | `ROUTES_ENABLED` | `service.DefaultRoutesPath` (`routes.yaml`) | `ROUTES_PATH` |

**Trigger logic** (same for every subsystem):
- Build the subsystem if `Enabled=true` **OR** the matching `Path` field is set.
- If `Path` is empty and `Enabled=true`, use the default const.
- Override `Path` always wins.

**Missing files:**
- Explicit `Path` overrides are strict — a missing file produces `service_*_yaml_not_found`.
- Default paths (via `Enabled=true`) are strict for apimap and routes (single file).
- NATSMap default paths are silent-skip on miss — supports publish-only and subscribe-only services that only drop one of the two files. If both default files are missing, returns `service_natsmap_yaml_not_found`.

## OpenAPI from routes.yaml

Declare the OpenAPI document metadata next to your routes:

```yaml
groups:
  - prefix: /v1
    routes: [...]

openapi:
  info:
    title: My API
    version: 1.0.0
    description: Public REST API.
    contact:
      name: Maintainer
      email: maintainer@example.com
  servers:
    - url: https://api.example.com
      description: production
  security_schemes:
    BearerAuth:
      type: http
      scheme: bearer
      bearer_format: JWT
  middleware_security:
    auth: [BearerAuth]
```

When this block is present, `service` mounts `/openapi.json` and `/docs`
automatically. Call `service.WithOpenAPI()` to opt in explicitly without
a YAML block (uses openapi package defaults), or pass openapi options to
override or augment YAML values:

```go
service.WithOpenAPI(
    openapi.WithInfo(openapi.Info{Title: "Override", Version: "2"}),
    openapi.WithDefaultResponse(404, ErrorResp{}),
)
```

**Precedence:** YAML applies first. Then user opts. `Info`: last-write-wins
(code overrides). `Servers` / `SecuritySchemes` / `MiddlewareSecurity`:
accumulating append.

**Out of scope for YAML:** `WithDefaultResponse(status, model)` and the
typed-schema builders (`gen.OnHandler(...).Body(...).Response(...)`) need
Go types — pass them via the option chain.

### Code-driven vs env-driven enable

Two equivalent ways to opt in:

- **Code:** pass `service.WithAPIMap()` / `WithNATSMap()` / `WithRoutes()` to `service.New`. Best when `main.go` already chains other `With*` options.
- **Env:** set `APIMAP_ENABLED=true` / `NATSMAP_ENABLED=true` / `ROUTES_ENABLED=true`. Best for env-driven deployments where Go-side flags would be awkward.

Both flip the same internal flag; pass either or both — both setting `Enabled = true` is idempotent.

## Options

| Option | Notes |
|---|---|
| `WithOpenAPI(opts ...openapi.Option)` | Enable OpenAPI mounting. With no args, Info/Servers/SecuritySchemes/MiddlewareSecurity come from `routes.yaml`'s top-level `openapi:` block. Pass `openapi.WithInfo(...)` / `WithServer(...)` / `WithSecurity(...)` / `WithDefaultResponse(...)` to override or augment. Auto-mounts even without this call when the YAML block is present. |
| `WithLogger(*slog.Logger)` | Override the auto-built logger |
| `WithMetrics(prometheus.Registerer)` | Override the default `prometheus.NewRegistry()` |
| `WithoutRuntimeMetrics()` | Skip auto-registration of `go_*` runtime + `process_*` collectors on the service registry. Use when the caller already registered them, or to keep the scrape output kit-only. |
| `WithValidator(bind.Validator)` | Override the default `validator.New(validator.WithRequiredStructEnabled())`. Use to register custom validators (`v.RegisterValidation("safe_url", …)`) or swap implementations entirely. |
| `WithFiberMiddleware(handlers...)` | Insert fiber-level middleware before engine (helmet, otelfiber, …) |
| `WithCORS(origins...)` | Shortcut for `fiber/v2/middleware/cors` with kit defaults: REST methods, common headers, `X-Request-ID` exposed, MaxAge 24h. Credentials on for explicit origins; auto-off when `"*"` is listed (CORS spec). |
| `WithCORSConfig(cors.Config)` | Full-control CORS — `cfg` is passed straight to `cors.New`. |
| `WithoutBearerOptionalLayer()` | Skip the auto `Bearer(BearerOptional)` install |
| `WithRefreshGC(interval)` | Schedule periodic `RefreshStore.GarbageCollect` against the auth refresh store so expired tokens get pruned. INFO log per non-zero sweep; WARN on failure. Bound to `OnShutdown` for clean stop. Interval ≤ 0 = disabled. No-op when Auth isn't configured. |
| `WithOtel(serviceName, otelkit.Option...)` | Enables OpenTelemetry tracing. Initializes a TracerProvider via OTLP/HTTP (`otelkit.Setup`), prepends `otelfiber` middleware (inbound spans), wraps httpc's base transport in `otelhttp` (outbound spans + W3C propagation), registers shutdown via `OnShutdown`. Configure exporter via standard `OTEL_EXPORTER_OTLP_*` env vars. See [otelkit](../otelkit/README.md). |
| `WithoutConnectRetry()` | Disables the auto-injected K8s-friendly retry defaults for DB and NATS Connect. Without this, service defaults to 5 retries with 1s→16s exponential backoff (~31s budget). See db/README and clients/nats/README. |
| `WithHTTPCOptions(opts...)` | Extra httpc options (logger + metrics already auto-applied) |
| `WithAPIMapOptions(opts...)` | Extra apimap options |
| `WithAPIMap()` | Equivalent to `Config.APIMap.Enabled = true`. Apimap auto-builds from `service.DefaultAPIMapPath` (`clients.yaml`) when no `Path` override is set. Missing file → `service_apimap_yaml_not_found`. |
| `WithAPIMapRegistration(fn)` | Register typed Request/Response models BEFORE `apimap.Build` seals the engine |
| `WithAPIMapEnv(m map[string]string)` | Explicit `${VAR}` values for apimap's clients.yaml. Map consulted before `os.LookupEnv`. |
| `WithNATSMap()` | Equivalent to `Config.NATSMap.Enabled = true`. Natsmap auto-builds from default subscribers/publishers paths. Requires NATS. |
| `WithNATSMapRegistration(fn)` | Register typed subscriber handlers + publishers via `natsmap.RegisterHandler[T]` / `natsmap.RegisterPublisher[T]` BEFORE `natsmap.Build` opens subscriptions. Required when `NATSMap.*Path` is set. |
| `WithNATSMapEnv(m map[string]string)` | Explicit `${VAR}` values for natsmap's subscribers/publishers YAML. Map consulted before `os.LookupEnv`. |
| `WithRoutes()` | Equivalent to `Config.Routes.Enabled = true`. Routes auto-load in `svc.Run()` from `service.DefaultRoutesPath` (`routes.yaml`). |
| `WithNATSOptions(opts...)` | Extra natsclient options |
| `WithRunOptions(opts...)` | Append `fibermap.RunOption`s to the default production-ops bundle |

## Common patterns

### Composing your own app config

```go
type Config struct {
    service.Config
    ShortURLBase string `env:"SHORT_URL_BASE" envDefault:"http://localhost:3000"`
}

var cfg Config
_ = env.Parse(&cfg)
svc, _ := service.New[AppCtx, Claims](ctx, cfg.Config)
```

### Registering typed apimap response models

```go
svc, _ := service.New[AppCtx, Claims](ctx, cfg,
    service.WithAPIMapRegistration(func(e *apimap.Engine) {
        apimap.RegisterResponse[User](e, "github.get_user")
        apimap.RegisterRequest[NewIssue](e, "github.create_issue")
        apimap.RegisterResponse[Issue](e, "github.create_issue")
    }),
)
```

Without this, `apimap.Decode[User]` returns generic JSON. After `Build` runs (inside `service.New`), the engine is sealed — registrations must happen via this option.

### Injecting otelhttp / helmet

```go
svc, _ := service.New[AppCtx, Claims](ctx, cfg,
    service.WithFiberMiddleware(
        helmet.New(),
        cors.New(cors.Config{AllowOrigins: "*"}),
    ),
)
```

The fiber-level middlewares run BEFORE the engine's contextInit, alongside the auto-installed `Bearer(BearerOptional)` layer.

### Custom cleanup via `OnShutdown`

`svc.Close()` only tears down what Service built. For app-specific resources (workers, third-party clients, Sentry / metrics pushers, scheduled jobs) register a callback:

```go
svc, _ := service.New[AppCtx, Claims](ctx, cfg)
defer svc.Close()

worker := startWorker(svc.DB)
svc.OnShutdown(worker.Stop)

scheduler := startScheduler()
svc.OnShutdown(scheduler.Shutdown)
```

Callbacks run on `Close()`:
1. **First**, registered callbacks fire in LIFO order. Kit subsystems (DB, NATS, …) are still alive so callbacks can flush in-flight state.
2. Then `NATSMap.Drain()` (in-flight handlers finish).
3. Then `NATS.Close()`.
4. Finally `DB.Close()`.

Errors returned by a callback are logged via `svc.Logger()` and do NOT abort subsequent callbacks or subsystem teardown. `OnShutdown` is thread-safe; calling it after `Close` is a no-op.

### Going around Service for one operation

Service exposes all deps as public fields — drop down whenever you need fine control:

```go
err := svc.DB.Tx(ctx, func(tx *db.Tx) error {
    // multi-statement transaction
    return nil
})

pub := natsclient.NewPublisher[MyEvent](svc.NATS)
pub.Publish(ctx, "my.event", MyEvent{...})

resp, _ := svc.HTTPC.Get("https://example.com")
```

## Error model

`service.New` returns `*errs.Error` with `Kind`/`Code`:

| Code | Kind | When |
|---|---|---|
| `service_auth_needs_db` | Validation | Auth configured but DB not |
| `service_auth_invalid_key` | Validation | PEM unparseable or wrong algorithm |
| `service_db_connect_failed` | Unavailable | `db.Connect` failed (wrapped) |
| `service_apimap_load_failed` | Validation | apimap LoadFile / Build failed (wrapped) |
| `service_nats_connect_failed` | Unavailable | `natsclient.Connect` failed (wrapped) |
| `service_natsmap_needs_nats` | Validation | NATSMap configured but NATS not |
| `service_natsmap_load_failed` | Validation | natsmap LoadFile / Build failed (wrapped) |
| `service_httpc_new_failed` | Validation | `httpc.New` validation failed (wrapped) |
| `service_openapi_mount_failed` | Internal | OpenAPI Mount failed |

Subsystem-specific errors propagate as `Cause` — use `errors.As` to extract.

## Observability

- `svc.Logger()` returns the `*slog.Logger` every subsystem was given.
- `svc.Metrics()` returns the `prometheus.Registerer` every subsystem registers into.
- All subsystems' `WithLogger`/`WithMetrics` options are auto-applied; you don't pass them per call.
- **Unified `/metrics` scrape.** `svc.Run()` routes the `/metrics` endpoint through the same registry, so a single scrape exposes `fibermap_http_*` (router), `db_*`, `httpc_*`, `nats_*`, `natsmap_*` together. The `go_*` (heap, GC, goroutines) and `process_*` (FDs, RSS, CPU seconds) runtime collectors are auto-registered on the same registry — disable with `service.WithoutRuntimeMetrics()`.
- **apimap metrics** ship under their own `apimap_*` namespace (`apimap_requests_total`, `apimap_request_duration_seconds`) with `client`+`endpoint`+`status` labels, so per-upstream visibility lands on the shared registry without colliding with the kit's `httpc_*` collectors. apimap no longer forwards the registry to its internal httpc clients; if you need both apimap-level and per-attempt httpc views, build a dedicated httpc with a separate registry outside apimap.

## Shutdown order

`svc.Close()` drains `NATSMap` (so in-flight subscriber handlers finish) **before** tearing down the `NATS` connection. Downstream subsystems (`DB`, `Auth`) close last. Always `defer svc.Close()` after `service.New`.

## Testing

For unit tests, the empty-config path builds a Service with only Engine + HTTPC:

```go
svc, _ := service.New[AppCtx, Claims](ctx, service.Config{})
```

For integration tests, mirror `examples/urlshort/main_test.go` — use testcontainers (Postgres + NATS), build a Service with full config, mount Engine on a `*fiber.App`, drive via `app.Test`.

## Limitations

- **`refreshpg` only.** No `refreshredis` selector — services that want Redis bypass Service for the auth ladder.
- **No migrations.** Apply your own SQL (`db.Exec(string(fileBytes))`) before registering handlers.
- **No background job runner.** Out of kit scope.
- **`New` is not concurrency-safe.** Construct once per process.
- **Service does not mirror every subpkg method.** Access subsystems via the public fields: `svc.DB.Tx(...)`, `svc.Auth.Sign(...)`, etc.
- **apimap metrics off by default** (see Observability above).

## See also

- [`fibermap`](../fibermap/README.md), [`errs`](../errs/README.md), [`db`](../db/README.md), [`auth`](../auth/README.md), [`clients/httpc`](../clients/httpc/README.md), [`clients/apimap`](../clients/apimap/README.md), [`clients/nats`](../clients/nats/README.md), [`clients/natsmap`](../clients/natsmap/README.md), [`fibermap/openapi`](../fibermap/openapi/README.md)
- [`examples/urlshort`](../examples/urlshort/README.md) — Service used end-to-end
