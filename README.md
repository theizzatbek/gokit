# fibermap

YAML-declarative router and middleware composer for [Fiber](https://github.com/gofiber/fiber).

- Describe your route tree in YAML.
- Register handlers and middleware by name (no reflection).
- Get a typed per-request context.

Status: **0.x — API unstable.**

## Install

```bash
go get github.com/theizzatbek/fibermap

# optional: standalone CLI for routes.yaml linting and schema export
go install github.com/theizzatbek/fibermap/cmd/fibermap@latest
```

Requires Go 1.23+ and Fiber v2.

## Editor support for `routes.yaml`

Add this single line to the top of your `routes.yaml`:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/theizzatbek/fibermap/main/schema/routes.schema.json
```

VS Code (with [redhat.vscode-yaml]), GoLand, and Vim with `coc-yaml`
then give you autocomplete for `method`/`middleware_sets`/etc, hover
documentation, and inline diagnostics — typos in `middleware:` get
highlighted before you ever run `go test`.

[redhat.vscode-yaml]: https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml

## CLI

```bash
fibermap validate routes.yaml    # schema-lint; non-zero exit on issues
fibermap dump-schema             # print the bundled JSON Schema
```

`validate` runs only the schema-level checks (required fields, valid
HTTP methods, middleware_set cycles, middleware shape). It does NOT
verify that handler/middleware/factory names are registered — your Go
binary is the only place those live. For full validation (including
registrations), use `Engine.Validate()` in a Go test or boot script.

## Examples

Two runnable examples — pick the one matching how you intend to use the lib:

- **[`examples/quickstart`](./examples/quickstart)** — minimal
  single-file demo (~100 LOC). Stub auth via `?role=` query, three
  inline handlers. Read this to *understand* fibermap.

  ```bash
  go run ./examples/quickstart
  curl -X POST 'http://localhost:3000/v1/patients?role=director'   # 201
  curl -X POST 'http://localhost:3000/v1/patients?role=guest'      # 403
  ```

- **[`examples/tasks`](./examples/tasks)** — realistic starting
  template. Multi-package layout under `internal/`, real Bearer-token
  auth, in-memory store behind a `Store` interface, request-id +
  structured `slog` logger, embedded `routes.yaml` via `embed.FS`,
  graceful shutdown, `/admin/routes` introspection endpoint, and
  `fibermaptest.AssertRoute` covering the route table. **Copy this
  directory** to start a new service.

  ```bash
  go run ./examples/tasks
  curl -H "Authorization: Bearer alice-token" http://localhost:3000/api/v1/tasks
  ```

## Lifecycle

```
New (or Default for the ops bundle)
    → SetContextBuilder
    → RegisterHandler / RegisterMiddleware / RegisterMiddlewareFactory
    → LoadFile / LoadBytes / LoadFS       (optional with Run — see below)
    → Validate                            (optional dry-run, no router needed)
    → Mount                               (one-shot; subsequent calls error)
    → app.Listen(":3000")
```

`Mount` validates everything against registered names and returns *all*
problems at once via `errors.Join`. No routes are installed if validation
fails. `Validate()` runs the same checks but doesn't touch any Fiber
router — handy for CI scripts or unit tests of `routes.yaml`.

`LoadFS(fs.FS, path)` accepts an `embed.FS` so the route definitions
can ship inside the binary:

```go
//go:embed routes.yaml
var routesFS embed.FS

eng.LoadFS(routesFS, "routes.yaml")
```

### Built-in `RequestID()` middleware

`fibermap.RequestID()` is a Fiber-level middleware (install via
`WithUse` or `app.Use`) that ensures every request carries an
`X-Request-ID`: it reads the incoming header, generates a fresh
16-hex-character identifier when missing, stashes the value on
`c.Locals(fibermap.LocalsRequestID)`, and echoes it back to the
response. Wire it before any auth middleware so 401s also carry the
ID:

```go
eng.Run(fibermap.WithUse(fibermap.RequestID(), auth.Bearer()))
```

The ContextBuilder then reads from the same locals key:

```go
rid, _ := c.Locals(fibermap.LocalsRequestID).(string)
```

### One-call launch via `Engine.Run`

For services that don't need anything special, `Engine.Run` wraps
`fiber.New` + `LoadFile("routes.yaml")` + `Mount` + `app.Listen(":3000")`
plus graceful shutdown on SIGINT/SIGTERM:

```go
eng := fibermap.New[AppCtx]()
eng.SetContextBuilder(...)
eng.RegisterHandler(...)
// no LoadFile, no Mount, no app.Listen — Run does it all.
if err := eng.Run(); err != nil {
    log.Fatal(err)
}
```

Defaults (all overridable via options):

| Default                                       | Customize / disable                                              |
| --------------------------------------------- | ---------------------------------------------------------------- |
| Listen on `:3000` (or `$PORT` env if set)      | `WithAddr(":8080")`                                              |
| Load `routes.yaml` from disk                   | `WithRoutesPath("api.yaml")` / `WithRoutesFS(embedFS)`           |
| `fiber.New()` with no config                   | `WithFiberConfig(fiber.Config{ErrorHandler: ...})`               |
| No extra Fiber-level middleware                | `WithUse(auth.Bearer())` (appended after the built-in RequestID) |
| **Recover** with `slog.Default()`              | `WithRecover(myLogger)` / `WithoutRecover()`                     |
| **RequestID** (built-in `X-Request-ID`)        | `WithoutRequestID()`                                             |
| **RequestLogger** with `slog.Default()`        | `WithRequestLogger(myLogger, skip...)` / `WithoutRequestLogger()` |
| **HealthCheck at `/healthz`**                  | `WithHealthCheck("/_health")` / `WithoutHealthCheck()`           |
| No metrics endpoint (heavy dep, opt-in)        | `WithMetrics("/metrics")` (or `fibermap.Default[T]()`)           |
| 10s graceful drain on signal                   | `WithShutdownTimeout(30*time.Second)` / `WithoutSignalHandling()` |
| Escape hatch: groups, sub-routes               | `WithConfigureApp(func(app *fiber.App) { ... })`                 |

Run skips loading if the engine already has a YAML document loaded —
useful when you preload from `LoadBytes` for tests or unusual layouts.

`$PORT` env support means a `Run()` call with no `WithAddr` works
out-of-the-box on Heroku, Cloud Run, fly.io, Railway, etc.

Mount errors, parse errors, and listen errors all surface as the
return value of `Run`. SIGINT/SIGTERM during normal operation
returns `nil` after a clean drain.

### Production-ready ops bundle — on by default

Since v0.5 the ops bundle is built into `Run` itself. `New[T]()` +
`Run()` already gives you:

- **`Recover`** with `slog.Default()` — panics → structured log + 500
- **`RequestID`** at the front of the Use chain — every response carries
  `X-Request-ID`
- **`RequestLogger`** with `slog.Default()`, skipping `/healthz` and
  `/metrics` — one structured access-log line per request
- **`HealthCheck`** at `/healthz` — bypasses auth/recover/ContextBuilder
  for k8s probes

`Metrics` is opt-in (it pulls in
`github.com/prometheus/client_golang`); use `fibermap.Default[T]()`
or `WithMetrics(path)` to enable it.

**To override a default**, call the matching `With*` option — it wins:

```go
eng.Run(
    fibermap.WithRecover(myLogger),                 // custom slog logger
    fibermap.WithRequestLogger(myLogger, "/h", "/m"),
    fibermap.WithHealthCheck("/_health"),           // move the path
)
```

**To suppress a default**, call the `Without*` option:

```go
eng.Run(
    fibermap.WithoutRecover(),         // handle panics yourself
    fibermap.WithoutRequestID(),       // you ship your own correlation header
    fibermap.WithoutRequestLogger(),   // log access via a different stack
    fibermap.WithoutHealthCheck(),     // probe lives elsewhere
)
```

### `fibermap.Default[T]()` — adds Metrics on top

`Default[T]()` is `New[T]()` plus an `Engine`-level default of
`WithMetrics("/metrics")` — use it when you want the full ops bundle
including the Prometheus endpoint without spelling it out:

```go
eng := fibermap.Default[AppCtx]()
eng.SetContextBuilder(...)
eng.RegisterHandler(...)
eng.Run(fibermap.WithUse(auth.Bearer()))   // full bundle + auth
```

To disable just metrics on a Default engine: `Run(WithoutMetrics())`.

### Programmatic routes via `Engine.Add`

Use `Engine.Add(method, path, name, handler, [AddOpts{...}])` for
routes that don't fit the declarative YAML model — debug/pprof,
dynamic admin handlers, embedded UIs:

```go
eng.Add("GET", "/debug/whoami", "debug.whoami",
    func(c *fibermap.Context[AppCtx]) error {
        return c.JSON(fiber.Map{"user_id": c.Data.UserID, "role": c.Data.Role})
    },
    fibermap.AddOpts{
        Description: "Echo the current caller's identity",
        Tags:        []string{"debug", "ops"},
    },
)
```

Programmatic routes go through the same per-request `Context[T]`
wrapper as YAML routes. They show up in `Engine.Routes()` with
`Source = SourceProgrammatic` so introspection tools, `fibermaptest`,
and OpenAPI generation see them.

Add does not accept middleware / cache / timeout — those features
are intentionally YAML-only to keep the declarative surface
authoritative. If you need middleware on a programmatic route, mount
it directly on the `*fiber.App` via `WithConfigureApp`.

Add panics (programmer error) on: invalid HTTP method, empty
name/path, nil handler, or being called after `Mount`.

If you need anything Run can't express (multiple servers, custom
signal sets, hot-reload), stick with the manual `LoadFile → Mount →
app.Listen` flow — they remain fully supported. Both
[`examples/quickstart`](./examples/quickstart) and
[`examples/tasks`](./examples/tasks) use `Run`; the tasks example
shows how `WithFiberConfig`, `WithUse`, and `WithRoutesFS` cover a
realistic production wire-up.

## Why

Hand-written `app.Get(...)` blocks duplicate three things: route shape, the
guard chain (`directorOnly`, `auth`, etc), and the boilerplate inside every
handler that re-extracts user/role from locals.

`fibermap` declares the first two in YAML and pre-builds the third into a
typed `Context[T].Data`.

## Quick start

```go
type AppCtx struct {
    UserID, OrgID, Role string
}

// Optional: hide the generic parameter behind project-local aliases so
// handler/middleware signatures read as `func(c *Ctx) error`.
type (
    Ctx = fibermap.Context[AppCtx]
    MW  = fibermap.MiddlewareFunc[AppCtx]
)

eng := fibermap.New[AppCtx]()

eng.SetContextBuilder(func(c *fiber.Ctx) (AppCtx, error) {
    return AppCtx{
        UserID: c.Locals("user_id").(string),
        OrgID:  c.Locals("organization_id").(string),
        Role:   c.Locals("role").(string),
    }, nil
})

eng.RegisterMiddleware("auth", authMW)
eng.RegisterMiddleware("audit", auditMW)
eng.RegisterMiddlewareFactory("require_role", func(args []string) (MW, error) {
    allowed := append([]string(nil), args...)
    return func(c *Ctx) error {
        for _, r := range allowed {
            if r == c.Data.Role { return c.Next() }
        }
        return c.Status(403).JSON(fiber.Map{"error": "forbidden"})
    }, nil
})
eng.RegisterHandler("patient.create", patient.Create)

if err := eng.LoadFile("routes.yaml"); err != nil { panic(err) }
if err := eng.Mount(app); err != nil { panic(err) }
```

```yaml
middleware_sets:
  protected: [auth]

groups:
  - prefix: /v1
    middleware_set: protected
    groups:
      - prefix: /patients
        routes:
          - { method: GET,  path: "",    handler: patient.list }
          - method: POST
            path: ""
            handler: patient.create
            middleware:
              - require_role: [director, receptionist]
          - method: PUT
            path: /:id
            handler: patient.update
            middleware:
              - require_role: [director]
              - audit
```

Handlers receive the typed context (with the `Ctx` alias from above):

```go
func (h *Patient) Create(c *Ctx) error {
    // c.Data.UserID is already populated by ContextBuilder
    // c.Status / c.JSON / etc. — all Fiber methods via embedding
    return c.Status(201).JSON(...)
}
```

## YAML reference

Top level:

| Field             | Type                | Notes                                          |
| ----------------- | ------------------- | ---------------------------------------------- |
| `middleware_sets` | `map[string][]MWRef` | Named bundles of middleware refs (plain or factory, see below). May reference other set names; recursively expanded. |
| `groups`          | `[]Group`           | Route tree.                                    |

Group:

| Field            | Type        | Notes                                                 |
| ---------------- | ----------- | ----------------------------------------------------- |
| `prefix`         | string      | Appended to ancestor prefix.                          |
| `middleware`     | `[]MWRef`   | Plain or parameterized middleware refs (see "Parameterized middleware"). |
| `middleware_set` | string      | Name from `middleware_sets`. Validated at mount.      |
| `routes`         | `[]Route`   |                                                       |
| `groups`         | `[]Group`   | Nested groups inherit prefix + middleware.            |

Route:

| Field            | Type        | Notes                                                 |
| ---------------- | ----------- | ----------------------------------------------------- |
| `method`         | string      | Required. `GET`/`POST`/`PUT`/`PATCH`/`DELETE`/`HEAD`/`OPTIONS`. |
| `path`           | string      | Fiber path pattern (`/:id`, wildcards, etc).          |
| `handler`        | string      | Required. Name registered via `RegisterHandler`.      |
| `middleware`     | `[]MWRef`   | Appended after ancestor chain. Plain or parameterized. |
| `middleware_set` | string      |                                                       |
| `name`           | string      | Free-form identifier; surfaced via `Routes()`. OpenAPI: `operationId`. |
| `summary`        | string      | Short one-line title; surfaced via `Routes()`. OpenAPI: `operation.summary`. |
| `description`    | string      | Free-form longer description; surfaced via `Routes()`. OpenAPI: `operation.description`. |
| `tags`           | `[]string`  | Free-form; surfaced via `Routes()`. OpenAPI: `operation.tags`. |
| `timeout`        | duration    | Go duration string (`"5s"`, `"300ms"`). When set, the route is wrapped with Fiber's `timeout.NewWithContext`: the handler's `UserContext()` deadline is set to this duration; on deadline a `context.DeadlineExceeded` returned from the handler surfaces as **408 Request Timeout**. Empty (default) means no per-route timeout. |
| `cache`          | duration / map | Enables built-in response caching. See "Response cache" below. |

`name`, `summary`, `description`, and `tags` are not interpreted by
the engine — they exist for introspection tooling and OpenAPI
generation.

## Middleware sets

A set is a named list of middleware refs (plain or parameterized). Sets may
reference other set names; resolution is recursive. The final chain for a
route is:

```
outermost ancestor group → … → route's own middleware
```

Duplicates are dropped, keeping the first occurrence. Two entries with the
same name but different args are NOT duplicates (e.g.
`require_role: [director]` and `require_role: [admin]` both run). Cycles
between set names are detected at parse time (`CodeMiddlewareCycle`); a
reference to an undefined set name fails at mount time
(`CodeUnknownMiddlewareSet`).

## Parameterized middleware

Any middleware that takes arguments registers as a factory. The factory is
called once per `(name, args)` tuple at `Mount` time and the resulting
middleware is cached for the lifetime of the engine.

```go
eng.RegisterMiddlewareFactory("require_role", func(args []string) (MW, error) {
    if len(args) == 0 {
        return nil, errors.New("require_role: at least one role required")
    }
    allowed := append([]string(nil), args...)
    return func(c *Ctx) error {
        for _, r := range allowed {
            if r == c.Data.Role { return c.Next() }
        }
        return c.Status(403).JSON(fiber.Map{"error": "forbidden"})
    }, nil
})
```

In YAML, a `middleware:` entry is either a scalar (plain middleware) or a
single-key map `{name: [args...]}` (factory call):

```yaml
middleware:
  - audit                          # plain (RegisterMiddleware)
  - require_role: [director]       # factory call (RegisterMiddlewareFactory)
```

The plain and factory registries do not overlap — a name registered one way
cannot be referenced as the other; the YAML form must match the
registration. If a factory returns an error from its setup, it surfaces as
`CodeInvalidFactoryArgs` in the joined `Mount` error.

## Per-route timeout

Add `timeout: 5s` to any route and fibermap wraps its handler with
Fiber's `timeout.NewWithContext`:

```yaml
routes:
  - method: GET
    path: /report
    handler: report.generate
    timeout: 30s
```

At request time, the handler's `c.UserContext()` is given a deadline
of `30s`. If the handler returns `context.DeadlineExceeded`, fibermap
surfaces it as **HTTP 408 Request Timeout**. Other errors pass through
unchanged. This is cooperative: handlers must respect `UserContext()`
(stdlib `net/http` and `database/sql` already do; long CPU loops won't
be interrupted).

Bad duration strings fail at `LoadFile`/`LoadBytes` with
`CodeInvalidTimeout`; zero or negative durations are rejected. The
verbatim YAML value is surfaced on `RouteInfo.Timeout` for
introspection.

## Request binding & validation

Subpackage `fibermap/bind` ships three generic helpers — `Body[T]`,
`Query[T]`, `Params[T]` — that combine Fiber's parser pass with a
validator pass. Typical request entry-point boilerplate, but typed
and one-liner:

```go
import (
    "github.com/go-playground/validator/v10"
    "github.com/theizzatbek/fibermap/bind"
)

var v = validator.New()

type CreateTaskReq struct {
    Title string `json:"title" validate:"required,min=1,max=200"`
}
type ListQuery struct {
    Limit  int    `query:"limit"  validate:"min=1,max=200"`
    Cursor string `query:"cursor"`
}
type TaskIDParams struct {
    ID string `params:"id" validate:"uuid"`
}

func (h *H) Create(c *Ctx) error {
    req, err := bind.Body[CreateTaskReq](c.Ctx, v)
    if err != nil {
        return c.Status(400).JSON(fiber.Map{"error": err.Error()})
    }
    ...
}

func (h *H) List(c *Ctx) error {
    q, err := bind.Query[ListQuery](c.Ctx, v)
    ...
}

func (h *H) Get(c *Ctx) error {
    p, err := bind.Params[TaskIDParams](c.Ctx, v)
    ...
}
```

The validator is injected via a one-method `Validator` interface
(`Struct(any) error`) — **fibermap does not depend on
`go-playground/validator`**. `*validator.Validate` satisfies the
interface as-is, but any custom validator (JSON Schema, hand-rolled,
...) works too. Pass `nil` to skip validation when you trust the
input shape.

Each helper has its own pair of sentinel errors so callers can branch
with `errors.Is`:

| Helper          | Parse error          | Validation error         |
| --------------- | -------------------- | ------------------------ |
| `bind.Body[T]`   | `bind.ErrParseBody`   | `bind.ErrValidateBody`   |
| `bind.Query[T]`  | `bind.ErrParseQuery`  | `bind.ErrValidateQuery`  |
| `bind.Params[T]` | `bind.ErrParseParams` | `bind.ErrValidateParams` |
| `bind.Header[T]` | `bind.ErrParseHeader` | `bind.ErrValidateHeader` |

For headers, use the `reqHeader:` struct tag (the convention Fiber's
`ReqHeaderParser` expects):

```go
type AuthHeader struct {
    Authorization string `reqHeader:"Authorization" validate:"required"`
    TraceID       string `reqHeader:"X-Trace-Id"`
}

h, err := bind.Header[AuthHeader](c.Ctx, v)
```

## Response cache

`cache` is a first-class route-level field — declare a TTL in YAML
and fibermap wraps the handler with Fiber's `cache` middleware
using engine-wide defaults you set once.

Two YAML shapes:

```yaml
# Scalar — TTL only.
- method: GET
  path: /reports
  handler: reports.list
  cache: 30s

# Mapping — full config.
- method: GET
  path: /products
  handler: products.list
  cache:
    ttl: 30s
    control: true                       # honour Cache-Control: no-store on requests
    headers: true                       # cache & replay response headers (ETag, X-Request-ID)
    vary_header: [Accept-Language]      # partition the cache by these request headers
```

Engine-wide defaults (storage backend, per-request key partitioning,
default in-memory cap) are set once on the engine:

```go
import (
    "github.com/gofiber/storage/redis/v3"
    "github.com/theizzatbek/fibermap"
)

store := redis.New(redis.Config{URL: "redis://localhost:6379"})

eng.SetCacheDefaults(fibermap.CacheDefaults[AppCtx]{
    Storage: store,
    KeyBy: func(c *fibermap.Context[AppCtx]) string {
        // SECURITY-critical for user-specific responses: without
        // KeyBy, two users sharing /tasks would share a cache entry.
        return c.Data.OrgID + ":" + c.Data.UserID
    },
})
```

`SetCacheDefaults` is optional — call it before `Mount`. Defaults:
Fiber's in-process map, no `KeyBy` (key is method + URL + vary
headers). The in-process map is fine for dev / single-instance;
production deployments should plug a shared `fiber.Storage` (Redis,
memcached, …) so replicas share one cache and restarts don't wipe
it.

**SECURITY:** if your handler returns user-specific data (e.g.
`/me`, `/v1/orders`) and you don't set `KeyBy`, one user's response
will be served to another. Always set `KeyBy` when caching anything
that depends on the authenticated user.

Cache key shape: `METHOD ORIGINAL_URL` + `|h:Name=value` for each
`vary_header` + `|d:fragment` for whatever `KeyBy` returns. Bad /
zero / negative `ttl` or empty `vary_header` entries fail at
`LoadFile`/`LoadBytes` with `CodeInvalidCache`.

The cache config is surfaced on `RouteInfo.Cache` for introspection
(JSON-friendly).

## OpenAPI 3.0 spec generation

Subpackage `fibermap/openapi` builds an OpenAPI 3.0 document from the
engine's introspection API — paths, methods, tags, descriptions,
and `operationId`s are pulled straight from `routes.yaml`. Per-handler
request/response types are attached via a fluent builder so the spec
becomes a single source of truth for both routing AND API
documentation:

```yaml
# routes.yaml — text-side metadata lives here
- method: POST
  path: /tasks
  handler: tasks.create
  name: tasks.create
  summary: Create a task
  description: Create a task for the caller
  tags: [tasks, write]
```

```go
// Go — typed schemas live here
import "github.com/theizzatbek/fibermap/openapi"

gen := openapi.NewGenerator(eng,
    openapi.WithInfo(openapi.Info{Title: "Tasks API", Version: "1.0.0"}),
    openapi.WithServer("https://api.example.com", "production"),
    openapi.WithSecurity("BearerAuth", openapi.HTTPBearer("JWT")),
    openapi.MapMiddlewareToSecurity("auth", "BearerAuth"),
)

gen.OnHandler("tasks.create").
    Body(CreateTaskReq{}).
    Response(201, Task{}).
    Response(400, ErrorResponse{})

spec, err := gen.Generate()         // []byte JSON
```

What's automatic — pulled straight from the route's YAML:

- `Path` translated from Fiber syntax to OpenAPI:
  `/users/:id/posts/:postId` → `/users/{id}/posts/{postId}` with the
  path parameters declared and marked `required: true`.
- `Name` → `operationId`.
- `Summary`, `Description`, `Tags` → operation metadata.
- Routes whose chain includes a middleware mapped via
  `MapMiddlewareToSecurity` get `security: [{name: []}]` attached.

What's opt-in via the builder:

- Request body schema — `OnHandler("name").Body(MyReq{})`.
- Query / header schemas — `.Query(...)` / `.Headers(...)`.
- Response schemas per status — `.Response(201, Task{})`. Pass `nil`
  to advertise an empty body (e.g. `Response(204, nil)`).

Text fields (`summary`, `description`, `tags`) are intentionally NOT
in the builder — they live in `routes.yaml`, alongside the route they
describe, so the YAML stays the single source of truth.

Schema reflection uses
[`invopop/jsonschema`](https://github.com/invopop/jsonschema) — Go
struct fields' `json:`, `validate:`, and `description` tags are
honoured. Reflected types are hoisted into
`components.schemas` and referenced via `$ref`.

### `gen.Mount()` — one-line wiring

To expose the spec at `/openapi.json` AND a docs viewer at `/docs`:

```go
gen := openapi.NewGenerator(eng, opts...)
gen.OnHandler("tasks.create").Body(CreateReq{}).Response(201, Task{})
// ...

if err := gen.Mount(); err != nil {
    log.Fatal(err)
}
```

That's it. `Mount` installs two programmatic routes on the engine via
`Engine.Add`:

- `GET /openapi.json` — spec (lazy-generated on first request, cached
  via `sync.Once`).
- `GET /docs` — Scalar HTML viewer pointing at `/openapi.json`.

Customize via `MountOpts` (zero value uses defaults shown above):

```go
gen.Mount(openapi.MountOpts{
    SpecPath:   "/api/openapi",
    DocsPath:   "/api/docs",
    DocsTitle:  "Tasks API",                // defaults to gen Info.Title
    DocsViewer: openapi.SwaggerUI,          // or .Redoc, or .Scalar
})
```

`Mount` must be called BEFORE `Engine.Run` / `Engine.Mount` — same
constraint as any `Engine.Add`.

If you need finer control (different caching, auth-gated docs,
multiple viewer mounts), skip `Mount` and call `gen.Generate()` /
`openapi.Scalar(...)` directly inside your own `Engine.Add` handlers.

### Browsable docs at `/docs`

Three HTML viewers are built in — each is a one-line helper that
returns a self-contained HTML page loading the viewer from a CDN and
pointing it at your `/openapi.json`. Pick whichever you prefer; or
mount more than one at different paths.

| Helper                          | Library                                                    | Notes                                            |
| ------------------------------- | ---------------------------------------------------------- | ------------------------------------------------ |
| `openapi.SwaggerUI(url, title)` | [Swagger UI](https://github.com/swagger-api/swagger-ui)     | Classic, full "try it out" requester.            |
| `openapi.Redoc(url, title)`     | [Redoc](https://github.com/Redocly/redoc)                   | Read-only, prettier for long specs.               |
| `openapi.Scalar(url, title)`    | [Scalar API Reference](https://github.com/scalar/scalar)    | Modern, dark-mode default, built-in API client.  |

```go
docs := openapi.Scalar("/openapi.json", "Tasks API")  // generate once at startup
eng.Add("GET", "/docs", "openapi.docs",
    func(c *fibermap.Context[AppCtx]) error {
        c.Set("Content-Type", "text/html; charset=utf-8")
        return c.SendString(docs)
    },
)
```

The HTML output is a static string — generate it once at startup,
serve as bytes per request. First page load fetches the JS bundle
from the CDN; subsequent loads use the browser cache.

User input is HTML-escaped, so passing values from config/env to
`title` is safe.

See [`examples/tasks/main.go`](./examples/tasks/main.go) for the full
wire-up (spec at `/openapi.json`, Scalar UI at `/docs`).

## Ready-made middleware factories

Subpackage `fibermap/factory` ships the factories every project ends
up writing by hand:

```go
import (
    "github.com/gofiber/fiber/v2/middleware/requestid"
    "github.com/theizzatbek/fibermap"
    "github.com/theizzatbek/fibermap/factory"
)

eng.RegisterMiddlewareFactory("require_role",
    factory.RequireRole(func(c *fibermap.Context[AppCtx]) string {
        return c.Data.Role
    }),
)

eng.RegisterMiddlewareFactory("require_scope",
    factory.RequireAnyScope(func(c *fibermap.Context[AppCtx]) []string {
        return c.Data.Scopes
    }),
)

// Bridge any plain fiber.Handler into the fibermap signature.
eng.RegisterMiddleware("request_id",
    factory.Adapter[AppCtx](requestid.New()),
)
```

| Helper              | What it does                                                                              |
| ------------------- | ----------------------------------------------------------------------------------------- |
| `RequireRole`        | Allows when the accessor's role is in the YAML args. Empty args rejected at Mount.        |
| `RequireAnyScope`    | Allows when the accessor's scopes intersect the YAML args (OAuth-any-of semantics).        |
| `Adapter`            | Wraps `fiber.Handler` into `MiddlewareFunc[T]`.                                            |
| `AdapterFactory`     | Wraps `func(args []string) (fiber.Handler, error)` into `MiddlewareFactoryFunc[T]`.        |

Both guards accept `factory.WithDenyHandler(h)` to override the default
`403 {"error":"forbidden"}` response.

The `fibermap.ContextFrom[T](c *fiber.Ctx)` helper exposed by the
core package gives you typed `Context[T]` access from inside any
`fiber.Handler` — use it when you write your own adapter that needs
to read `Data`.

## Introspection

After `Mount`, `Engine.Routes()` returns a snapshot of every installed route:

```go
for _, r := range eng.Routes() {
    fmt.Printf("%-6s %-30s -> %s  middleware=%v\n",
        r.Method, r.Path, r.Handler, r.Middleware)
}
```

`RouteInfo` carries `Method`, `Path`, `Handler`, `Name`, `Description`,
`Tags`, and `Middleware` — a `[]MiddlewareRef` where each entry holds the
middleware `Name` and its `Args` (nil for plain, the YAML list for factory
calls). All fields have `json:` tags so you can expose the slice over an
admin endpoint without an extra wrapper. The returned slice and each
entry's slice fields are independent copies — mutating them does not
affect engine state.

For walks with early-stop semantics or single-lookup queries:

```go
eng.Walk(func(r fibermap.RouteInfo) error {
    if strings.HasPrefix(r.Path, "/internal/") { return fibermap.ErrStopWalk }
    return nil
})

if r, ok := eng.Lookup("POST", "/v1/users"); ok {
    fmt.Println("handler:", r.Handler)
}
```

## Testing

Subpackage `fibermap/fibermaptest` ships assertion helpers that work
off the introspection API — **no `fiber.App` or HTTP roundtrip
required**:

```go
import "github.com/theizzatbek/fibermap/fibermaptest"

func TestRoutes(t *testing.T) {
    eng := buildEngineForTests(t)  // your Load* + Mount on a throwaway router

    fibermaptest.AssertRoute(t, eng, "POST", "/v1/things",
        fibermaptest.WithHandler("things.create"),
        fibermaptest.WithMiddleware("auth", "audit"),  // in-order subsequence
        fibermaptest.WithTags("things", "write"),
    )
    fibermaptest.AssertNoRoute(t, eng, "DELETE", "/v1/things")
    fibermaptest.AssertRouteCount(t, eng, 12)
}
```

Helpers call `Errorf` (not `Fatal`), so multiple assertions surface
all failures in one run.

## Mount caveat

`Mount(router)` installs a single root middleware on `router` via
`router.Use(...)` that builds your `Context[T]` once per request. This
means **every** route on that router — including routes added later
outside fibermap — will run the context builder. Usually fine; if you
need a router whose contextInit doesn't leak, mount fibermap on a
dedicated `app.Group("/api", ...)` sub-router.

## Error handling

- Register-time (programmer error): a duplicate name within or across
  the plain/factory registries panics with `*Error` /
  `CodeDuplicateRegistration`. Calling `Register*` after `Mount` panics
  with `CodeRegisterAfterMount` (registration after mount is silently
  useless otherwise — the map is consulted only during `buildPlan`).
  Registration follows the `MustCompile` convention; there is no return
  value to check.
- Parse-time errors (bad YAML, missing fields, invalid HTTP method, cycles in
  `middleware_sets`, malformed `middleware:` entry) — returned by
  `LoadFile`/`LoadBytes`.
- Mount-time errors (unknown handler/middleware/factory name, wrong YAML form
  for a registered name, duplicate `method+path`, no `ContextBuilder`,
  factory-args rejection) — accumulated and returned via `errors.Join` from
  `Mount`.
- Runtime: a failing `ContextBuilder` triggers `SetContextErrorHandler`
  (default 500). Authorization (`require_role`, etc) is just user-supplied
  middleware — it can return any status it wants. Handler-returned errors
  pass through to Fiber's normal `ErrorHandler`.

## License

MIT. See `LICENSE`.
