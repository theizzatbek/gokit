# fibermap

YAML-declarative router and middleware composer for [Fiber v2](https://github.com/gofiber/fiber). Describe routes in YAML, register handlers and middleware by name (no reflection), get a typed per-request context, and ship a service with `Run()` that bundles recover + request-id + slog + Prometheus + healthz out of the box.

**Import:** `github.com/theizzatbek/gokit/fibermap`
**Depends on:** `gofiber/fiber/v2`, `gopkg.in/yaml.v3`, `prometheus/client_golang`, `github.com/theizzatbek/gokit/errs`, `github.com/theizzatbek/gokit/fibermap/bind`

## Why use it

Hand-rolling Fiber bootstrap means re-deciding routes-vs-code coupling, middleware ordering, OpenAPI integration, panic recovery, metrics, healthchecks, and graceful shutdown on every service. fibermap moves all of that into one declarative file (routes.yaml) + a build-once-mount-once `Engine[T]` configurator. Three things span the package:

1. **The lifecycle is enforced.** `New[T]() → SetContextBuilder → Register* → LoadFile → Run` (or `Mount`). `Mount` validates everything together and returns `errors.Join` of all problems; nothing installs partially.
2. **Per-request `Context[T]` is built once and propagated.** Handlers see `*Context[T]` where `T` is your `AppCtx` carrying request-scoped data (user_id, request_id, logger). One ContextBuilder, then every handler reads typed state.
3. **Middleware chains are resolved declaratively.** `middleware_set` groups + per-route `middleware:` lists. Plain (`bearer`) and factory (`require_role: [admin]`) forms; sets recursively expand; duplicates deduped by `(name, args)`.

## Quickstart

`routes.yaml`:

```yaml
groups:
  - prefix: /v1
    routes:
      - method: GET
        path: /ping
        handler: ping
        name: ping.get
```

`main.go`:

```go
package main

import (
    "github.com/gofiber/fiber/v2"
    "github.com/theizzatbek/gokit/fibermap"
)

type AppCtx struct {
    UserID string
}

func main() {
    eng := fibermap.New[AppCtx]()
    eng.SetContextBuilder(func(c *fiber.Ctx) (AppCtx, error) {
        return AppCtx{}, nil
    })

    fibermap.RegisterHandler(eng, "ping", func(c *fibermap.Context[AppCtx]) error {
        return c.SendString("pong")
    })

    if err := eng.Run(fibermap.WithAddr(":3000")); err != nil {
        panic(err)
    }
}
```

That's it. The `Run` bundle gives you `/healthz`, `/metrics`, request-id, structured access logs, and panic recovery for free. Use `fibermap.Default[T]()` instead of `New[T]()` to also embed the ops bundle into the engine itself (auto-applied even in tests via `Mount`).

## Configuration

### Engine construction

| Function | What it gives |
|---|---|
| `New[T]() *Engine[T]` | bare engine; you opt in to every feature |
| `Default[T]() *Engine[T]` | engine with `WithRecover` + `WithRequestLogger` + `WithMetrics` + `WithHealthCheck` defaults pre-applied to Run |

### Engine setup methods

| Method | When you call it |
|---|---|
| `SetContextBuilder(fn ContextBuilder[T])` | Required. Build per-request `Context[T].Data` from `*fiber.Ctx` (read Bearer locals, request-id, etc.) |
| `SetValidator(v bind.Validator)` | Optional. Used by `RegisterHandlerWithBody/Query/Params/Headers` to validate decoded structs |
| `SetCacheDefaults(d CacheDefaults[T])` | Optional. KeyBy / Headers / Control defaults for the YAML `cache:` block |
| `SetBindErrorHandler(fn BindErrorFunc[T])` | Optional. Custom error mapping for bind failures (default: `errs.Validation`) |

### `Run` options (RunOption)

| Option | Default | Notes |
|---|---|---|
| `WithAddr(":3000")` | from `$PORT` env, else `:3000` | TCP listen address |
| `WithRoutesPath("routes.yaml")` | `routes.yaml` | Path passed to internal `LoadFile` if you skipped manual `LoadFile` |
| `WithRoutesFS(fs.FS)` | none | `embed.FS` source — bundle routes.yaml into the binary |
| `WithFiberConfig(fiber.Config)` | minimal | Custom `*fiber.App` config (override `ErrorHandler`, `BodyLimit`, etc.) |
| `WithUse(handlers ...fiber.Handler)` | `[RequestID]` | Fiber-level middleware installed BEFORE the engine's contextInit |
| `WithConfigureApp(fn func(*fiber.App))` | none | Hook to manipulate the `*fiber.App` after Mount |
| `WithShutdownTimeout(d)` | 10s | Graceful shutdown deadline on SIGINT/SIGTERM |
| `WithoutSignalHandling()` | — | Skip the built-in signal handler (caller manages shutdown) |
| `WithRecover(logger)` / `WithoutRecover()` | on (slog.Default) | Panic recovery with stack trace |
| `WithoutRequestID()` | request-id on | Inject `X-Request-ID` |
| `WithRequestLogger(logger, skipPaths...)` / `WithoutRequestLogger()` | on (skip `/healthz`,`/metrics`) | Structured access log |
| `WithMetrics(path)` / `WithoutMetrics()` | `/metrics` (only via `Default[T]`) | Prometheus endpoint |
| `WithMetricsRegistry(reg)` | private registry | Route middleware + scrape through caller-provided registry — unifies `fibermap_http_*` with the app's own collectors. |
| `WithHealthCheck(path)` / `WithoutHealthCheck()` | `/healthz` | Always-200 health endpoint, bypasses ContextBuilder |

## Common patterns

### YAML schema for a complete route

```yaml
groups:
  - prefix: /api/v1
    middleware:                       # group-level: applied to every nested route + sub-group
      - bearer: []                    # factory middleware: map form even with empty args
    groups:                           # nested groups inherit middleware
      - prefix: /tasks
        routes:
          - method: GET
            path: ""                  # empty = group prefix itself
            handler: tasks.list       # name registered via RegisterHandler
            name: tasks.list          # required: stable name for OpenAPI / Routes()
            tags: [tasks]             # optional: openapi tag(s)
            summary: List tasks
            description: List the caller's tasks
            middleware:               # route-level: appended AFTER group middleware
              - require_role: [admin]
            timeout: 5s               # optional: per-route timeout
            cache:                    # optional: response cache
              ttl: 10s
              control: true           # respect Cache-Control: no-store
              headers: true           # cache + replay response headers
```

### Typed body-bound handler

```go
type CreateTaskRequest struct {
    Title string `json:"title" validate:"required,min=1,max=200"`
}

type Task struct {
    ID    string `json:"id"`
    Title string `json:"title"`
}

fibermap.RegisterHandlerWithBody(eng, "tasks.create",
    func(c *fibermap.Context[AppCtx], req CreateTaskRequest) error {
        t := svc.Create(c.Data.UserID, req.Title)
        return c.Status(201).JSON(t)
    },
    fibermap.WithResponse(201, Task{}),     // for OpenAPI
    fibermap.WithResponse(400, errs.Response{}),
)
```

Sibling helpers: `RegisterHandlerWithQuery`, `RegisterHandlerWithParams`, `RegisterHandlerWithHeaders`. All run `eng.validator` against the decoded struct before invoking the handler. Bind failures bubble up as `*errs.Error{Kind: Validation}` mapped to 400.

### Combined binders — `RegisterHandlerWithInput`

When one endpoint needs more than one of `{body, params, query, headers}`
typed together — e.g. PATCH /things/:id with body, path id, and a query
filter — use `RegisterHandlerWithInput`. The Input struct declares any
combination of fields named exactly `Body`, `Params`, `Query`, `Headers`:

```go
type UpdateThingInput struct {
    Body   UpdateBody       // {"title": "...", "tags": [...]}
    Params struct {         // /things/:id
        ID string `params:"id" validate:"required,uuid"`
    }
    Query struct {          // ?notify=true
        Notify bool `query:"notify"`
    }
}

fibermap.RegisterHandlerWithInput(eng, "things.update",
    func(c *fibermap.Context[AppCtx], in UpdateThingInput) error {
        // in.Body, in.Params, in.Query already parsed + validated.
        return c.JSON(svc.Update(in.Params.ID, in.Body, in.Notify))
    })
```

The kit reflects on Input **once at registration**, builds the binder list,
and re-uses it per request — no reflection cost in the hot path beyond a
field index lookup per recognised field. Fields with names outside the
reserved set are ignored.

Each recognised field auto-attaches its matching `With*` option, so OpenAPI
generation sees the full set of schemas without the caller threading any
opts. Validation flows through `eng.validator` exactly as for the
single-source variants.

**Misuse panics at registration:**
- `Input` is not a struct.
- No recognised field (use plain `RegisterHandler` instead).
- A recognised field whose type is not a struct.

### Factory middleware (parameterised)

```go
// At registration time
fibermap.RegisterMiddlewareFactory(eng, "require_role",
    func(args []string) (fibermap.MiddlewareFunc[AppCtx], error) {
        roles := args
        return func(c *fibermap.Context[AppCtx]) error {
            if !slices.Contains(roles, c.Data.Role) {
                return errs.Permission("forbidden", "missing required role")
            }
            return c.Next()
        }, nil
    })

// In routes.yaml
middleware:
  - require_role: [admin]
  - require_role: [editor, owner]   # different args = separate handler in dedup cache
```

### Mounting on an existing *fiber.App (tests + composability)

```go
app := fiber.New(fiber.Config{
    ErrorHandler: fibermap.ErrorHandler(logger),  // wires errs.HTTP into Fiber
})
app.Use(authObj.Bearer(auth.BearerOptional))      // pre-engine middlewares
if err := eng.Mount(app); err != nil {
    return err
}
resp, _ := app.Test(httptest.NewRequest("GET", "/v1/ping", nil), -1)
```

`Mount` is the only way to use the engine without `Run` — needed for in-process tests with `app.Test`.

### Programmatic routes (raw Fiber handlers)

```go
eng.Add("POST", "/auth/refresh", "auth.refresh",
    func(c *fibermap.Context[AppCtx]) error {
        return authObj.IssueRefresh(c.Ctx)  // wrap a raw *fiber.Ctx handler
    })
```

Programmatic routes participate in OpenAPI generation and the engine's ContextBuilder + middleware chain. They cannot carry YAML middleware (use `WithUse` or wrap manually).

## Error model

Every error returned by the library is `*fibermap.Error` (an alias around the package's own typed error type) with `Stage` (`parse` / `mount` / `register`) and a `Code*` constant. New error conditions add a `Code*` constant. Mount-stage errors are accumulated into one `errors.Join` so all problems surface in a single call.

Use `fibermap.ErrorHandler(logger)` as the `fiber.Config.ErrorHandler` to wire `errs.HTTP` for handler errors and fall back to `*fiber.Error`'s own code for router-level (404/405) errors. Auto-logs 5xx via the passed logger; 4xx is silent by default.

## Observability

`fibermap.Default[T]()` (or `Run` with the matching options) ships:

- **slog access log** with method, path, status, duration_ms, request_id, response_size
- **Prometheus metrics** at `/metrics` — `http_requests_total{method,path,status}`, `http_request_duration_seconds`, in-flight gauge
- **Health endpoint** at `/healthz` — bypasses ContextBuilder so it works even when auth/db is down
- **Request ID** propagated as `X-Request-ID` header + stored in `c.Locals(fibermap.LocalsRequestID)`

Pass a `*slog.Logger` to `WithRecover`, `WithRequestLogger`. nil = `slog.Default()`.

## Testing

Use `fibermap/fibermaptest` for assertions over `Engine.Routes()` (route inventory checks). For request-level tests, use `Engine.Mount(app)` on a fresh `*fiber.App` and drive `app.Test(req)`.

```go
func TestRoutes(t *testing.T) {
    eng := buildEngine(t)                    // your setup
    app := fiber.New(fiber.Config{ErrorHandler: fibermap.ErrorHandler(nil)})
    if err := eng.Mount(app); err != nil { t.Fatal(err) }

    resp, _ := app.Test(httptest.NewRequest("GET", "/v1/ping", nil), -1)
    require.Equal(t, 200, resp.StatusCode)
}
```

## Limitations

- **No built-in rate-limiting.** Use `gofiber/fiber/v2/middleware/limiter` via `WithUse`.
- **No hot-reload of routes.yaml.** Loaded once at startup.
- **No per-route auth declarative shorthand.** Use middleware factories registered via `auth/fibermount.MountMiddlewareFactories`.
- **YAML errors at parse time, not edit time.** Use the routes.schema.json (see `fibermap/schema/`) in your editor for live validation.
- **`Mount`/`Run` can be called only once per engine.** Re-mounting is a programmer error (panics).

## See also

- [`fibermap/bind`](bind/README.md) — request body/query/header/params decoding + validation
- [`fibermap/factory`](factory/README.md) — helpers for building middleware factories
- [`fibermap/fibermaptest`](fibermaptest/README.md) — testing helpers for Routes() inventory
- [`fibermap/openapi`](openapi/README.md) — OpenAPI 3.0 spec generation from `Engine.Routes()`
- [`fibermap/schema`](schema/README.md) — embedded JSON schema for routes.yaml
- [`auth/fibermount`](../auth/fibermount/README.md) — mounts `bearer`/`require_scope`/`require_role` factories onto the engine
- [`errs`](../errs/README.md) — the typed-error contract used by `ErrorHandler`
- [`examples/urlshort/`](../examples/urlshort/README.md) — full integration example
