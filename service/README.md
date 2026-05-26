# service

All-in-one service helper. One `service.New(ctx, cfg)` builds the bundled runtime — `*db.DB`, `*auth.Auth[C]`, `*natsclient.Client`, `*natsmap.Runtime`, `*http.Client`, `*apimap.Client`, `*fibermap.Engine[T]` — with auto-detect optionality (subsystems with empty config stay nil). Auto-mounts `/auth/login` `/auth/refresh` `/auth/logout` when Auth configured. Auto-installs `auth.Bearer(BearerOptional)` at fiber.App level via `WithUse` so `ContextBuilder` reads JWT subject correctly (fixes a real gotcha). `Run()` blocks with the production-ops bundle. Service is additive over the existing subpackages — go straight to `svc.DB.Tx(...)` / `svc.Auth.Sign(...)` for anything Service doesn't shortcut.

**Import:** `github.com/theizzatbek/gokit/service`
**Depends on:** every other `gokit/*` subpackage

## Why use it

Wiring a kit-based service hand-rolls ~200 lines: `KeySet` from PEM, `auth.New` + `refreshpg.New` plumbing, `httpc.New`, `apimap.New + LoadFile + Build` (with the `${MICROLINK_BASE_URL}` env trick), `natsclient.Connect`, `fibermap.Default + SetValidator`, `fibermount.MountMiddlewareFactories`, wrap each of the three `auth.{Login,Refresh,Logout}Handler` as fibermap programmatic routes, install `Bearer(BearerOptional)` at fiber.App level via `WithUse` (or quietly hit the "AppCtx.UserID is empty in handlers" trap), assemble `RunOption`s, manage graceful shutdown, set up `slog`. `service` is that bundle.

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
    svc.SetCredentialsVerifier(func(ctx context.Context, req auth.LoginRequest) (auth.LoginResult[Claims], error) {
        // your verifier — look up user, check password
        return auth.LoginResult[Claims]{Subject: "uid", Custom: Claims{Email: req.Login}}, nil
    })

    fibermap.RegisterHandler(svc.Engine, "ping", func(c *fibermap.Context[AppCtx]) error {
        return c.SendString("pong")
    })

    if err := svc.Run(); err != nil { log.Fatal(err) }
}
```

## Configuration

Env-driven via `caarlos0/env/v11`. Compose into your own app config via embedding to add app-specific fields.

### Top-level `service.Config`

| Section | Prefix | Trigger | Notes |
|---|---|---|---|
| `Service` | (none) | always | `ADDR`, `LOG_LEVEL`, `LOG_FORMAT` |
| `DB` | `DB_` | `DB_USER` set | When omitted, `svc.DB == nil` |
| `Auth` | `AUTH_` | `AUTH_PRIVATE_KEY_PEM` set | Requires DB (refreshpg store) |
| `NATS` | `NATS_` | `NATS_URL` set | Independent |
| `NATSMap` | `NATSMAP_` | `NATSMAP_SUBSCRIBERS_PATH` or `NATSMAP_PUBLISHERS_PATH` set | Requires NATS |
| `HTTPC` | `HTTPC_` | always | Zero-value → sensible defaults |
| `APIMap` | `APIMAP_` | `APIMAP_PATH` set | Path to clients.yaml |

### `ServiceConfig`

| Field | Env | Default |
|---|---|---|
| `Addr` | `ADDR` | `:3000` |
| `LogLevel` | `LOG_LEVEL` | `info` |
| `LogFormat` | `LOG_FORMAT` | `json` (also: `text`) |

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
| `Path` | `APIMAP_PATH` |

### `NATSMapConfig`

| Field | Env |
|---|---|
| `SubscribersPath` | `NATSMAP_SUBSCRIBERS_PATH` |
| `PublishersPath` | `NATSMAP_PUBLISHERS_PATH` |

Either path triggers auto-build via `clients/natsmap`. Both paths may point at the same combined YAML. Requires `NATS` to be configured (`service_natsmap_needs_nats` otherwise).

## Options

| Option | Notes |
|---|---|
| `WithOpenAPI(info, opts...)` | Mounts `/openapi.json` + `/docs` (Scalar UI) |
| `WithLogger(*slog.Logger)` | Override the auto-built logger |
| `WithMetrics(prometheus.Registerer)` | Override the default `prometheus.NewRegistry()` |
| `WithFiberMiddleware(handlers...)` | Insert fiber-level middleware before engine (helmet, cors, otelfiber, …) |
| `WithoutAuthHandlers()` | Skip auto-mount of `/auth/login` `/refresh` `/logout` |
| `WithoutBearerOptionalLayer()` | Skip the auto `Bearer(BearerOptional)` install |
| `WithHTTPCOptions(opts...)` | Extra httpc options (logger + metrics already auto-applied) |
| `WithAPIMapOptions(opts...)` | Extra apimap options |
| `WithAPIMapRegistration(fn)` | Register typed Request/Response models BEFORE `apimap.Build` seals the engine |
| `WithAPIMapEnv(m map[string]string)` | Explicit `${VAR}` values for apimap's clients.yaml. Map consulted before `os.LookupEnv`. |
| `WithNATSMapRegistration(fn)` | Register typed subscriber handlers + publishers via `natsmap.RegisterHandler[T]` / `natsmap.RegisterPublisher[T]` BEFORE `natsmap.Build` opens subscriptions. Required when `NATSMap.*Path` is set. |
| `WithNATSMapEnv(m map[string]string)` | Explicit `${VAR}` values for natsmap's subscribers/publishers YAML. Map consulted before `os.LookupEnv`. |
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

### Disabling auto-mounted auth handlers

```go
svc, _ := service.New[AppCtx, Claims](ctx, cfg, service.WithoutAuthHandlers())
// Now mount your own auth routes via svc.Engine.Add(...) or a custom routes.yaml.
```

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
- **Note:** `apimap` does NOT receive `WithMetrics` automatically — apimap internally constructs its own `httpc` clients which would re-register the same `httpc_*` collectors and panic the shared registry. If you want per-upstream apimap metrics, pass `apimap.WithMetrics(separateReg)` via `WithAPIMapOptions`.

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
