# fibermap/openapi

OpenAPI 3.0 spec generation + UI mounting from a `*fibermap.Engine[T]`. Reflects request/response models registered via `fibermap.WithBody`/`WithQuery`/`WithHeaders`/`WithParams`/`WithResponse` HandlerOptions, walks `Engine.Routes()`, emits a JSON spec, and (via `Generator.Mount`) installs `/openapi.json` + a `/docs` HTML viewer (Scalar UI by default).

**Import:** `github.com/theizzatbek/gokit/fibermap/openapi`
**Depends on:** `github.com/theizzatbek/gokit/fibermap`

## Why use it

Writing OpenAPI by hand is a chore: routes and schemas duplicate code that's already in your handlers + routes.yaml. fibermap already has the route table (`Engine.Routes()`) and the typed-handler registration knobs (`WithBody`, `WithResponse`). `openapi.NewGenerator(eng).Mount()` joins them and gives you `/openapi.json` + `/docs` with no maintenance burden — and `tags`/`summary`/`description` from your routes.yaml flow straight in.

## Quickstart

```go
import (
    "github.com/theizzatbek/gokit/fibermap"
    "github.com/theizzatbek/gokit/fibermap/openapi"
)

eng := fibermap.Default[AppCtx]()

// Register handlers with body/response schemas for OpenAPI to reflect.
fibermap.RegisterHandlerWithBody(eng, "tasks.create",
    func(c *fibermap.Context[AppCtx], req CreateRequest) error {
        return c.Status(201).JSON(Task{})
    },
    fibermap.WithResponse(201, Task{}),
    fibermap.WithResponse(400, errs.Response{}),
)

eng.LoadFile("routes.yaml")

// One call to mount /openapi.json + /docs
gen := openapi.NewGenerator(eng,
    openapi.WithInfo(openapi.Info{
        Title:       "Tasks API",
        Version:     "0.1.0",
        Description: "Per-user task lists.",
    }),
    openapi.SecurityMapping("BearerAuth", openapi.HTTPBearer(), "bearer"),
)
if err := gen.Mount(); err != nil { return err }

eng.Run(fibermap.WithAddr(":3000"))
// → curl http://localhost:3000/openapi.json
// → open http://localhost:3000/docs    (Scalar UI)
```

## Public API

```go
type Generator[T any] struct{ /* unexported */ }

func NewGenerator[T any](eng *fibermap.Engine[T], opts ...Option) *Generator[T]

// Mount installs /openapi.json + /docs as programmatic routes on the
// engine. Must be called BEFORE eng.Mount/Run.
func (g *Generator[T]) Mount(opts ...MountOpts) error

// Generate returns the raw JSON bytes — useful for `fibermap dump-openapi`
// style tooling or writing the spec to a file at build time.
func (g *Generator[T]) Generate() ([]byte, error)
```

```go
type MountOpts struct {
    SpecPath   string  // default "/openapi.json"
    DocsPath   string  // default "/docs"
    Viewer     Viewer  // ScalarUI (default) | SwaggerUI | Redoc | NoViewer
    SpecURL    string  // only when Viewer != NoViewer; default uses SpecPath
}
```

## Options

| Option | Notes |
|---|---|
| `WithInfo(Info{Title, Version, Description, Contact})` | OpenAPI `info` block — set per service |
| `WithServer(url, description)` | Adds an entry to `servers[]`; call multiple times for prod/staging |
| `WithSecurity(name, SecurityScheme)` | Defines a `components.securitySchemes` entry (HTTPBearer/HTTPBasic/APIKey/OAuth2) |
| `MapMiddlewareToSecurity(middleware, schemeName)` | Tells the generator "routes with this middleware require this security scheme" |
| `SecurityMapping(schemeName, scheme, middlewares...)` | Convenience: `WithSecurity` + `MapMiddlewareToSecurity` in one call |
| `WithDefaultResponse(status int, model any)` | Adds a default response (e.g. 400/401/403/404/500 = errs.Response{}) to every operation that doesn't override |

## Common patterns

### Wiring security schemes

```go
gen := openapi.NewGenerator(eng,
    openapi.SecurityMapping("BearerAuth", openapi.HTTPBearer("JWT"), "bearer"),
    openapi.SecurityMapping("BasicAuth",  openapi.HTTPBasic(),       "basic"),
    openapi.SecurityMapping("ApiKey",     openapi.APIKey("X-API-Key", "header"), "api_key"),
)
```

Routes whose middleware list contains `bearer` automatically get `security: [{BearerAuth: []}]` in the generated spec.

### Default error responses for every operation

```go
gen := openapi.NewGenerator(eng,
    openapi.WithDefaultResponse(400, errs.Response{}),
    openapi.WithDefaultResponse(401, errs.Response{}),
    openapi.WithDefaultResponse(403, errs.Response{}),
    openapi.WithDefaultResponse(404, errs.Response{}),
    openapi.WithDefaultResponse(500, errs.Response{}),
)
```

Then in handler registrations you only declare the success response:

```go
fibermap.RegisterHandlerWithBody(eng, "tasks.create",
    func(c *fibermap.Context[AppCtx], req CreateRequest) error { /* … */ },
    fibermap.WithResponse(201, Task{}),  // success only — defaults fill the rest
)
```

### Choosing the docs UI

```go
gen.Mount(openapi.MountOpts{Viewer: openapi.ScalarUI})  // default — modern, fast
gen.Mount(openapi.MountOpts{Viewer: openapi.SwaggerUI})
gen.Mount(openapi.MountOpts{Viewer: openapi.Redoc})
gen.Mount(openapi.MountOpts{Viewer: openapi.NoViewer})   // /openapi.json only
```

### Custom mount paths

```go
gen.Mount(openapi.MountOpts{
    SpecPath: "/api/openapi.json",
    DocsPath: "/api/docs",
})
```

### Multiple environments — `servers[]`

```go
openapi.NewGenerator(eng,
    openapi.WithServer("https://api.prod.example.com", "production"),
    openapi.WithServer("https://api.staging.example.com", "staging"),
    openapi.WithServer("http://localhost:3000", "local"),
)
```

### Build-time spec dump (CI integration)

```go
data, err := gen.Generate()
_ = os.WriteFile("openapi.json", data, 0644)
// Now diff against committed openapi.json in CI to catch unintended API changes
```

## What gets reflected

| Route metadata | Source |
|---|---|
| `paths[<path>].<method>.summary` | YAML `summary:` |
| `paths[<path>].<method>.description` | YAML `description:` |
| `paths[<path>].<method>.tags` | YAML `tags:` (array) |
| `paths[<path>].<method>.operationId` | YAML `name:` |
| Request body schema | `fibermap.WithBody(StructType{})` HandlerOption |
| Query parameters | `fibermap.WithQuery(StructType{})` — fields with `query:"name"` tag |
| Path parameters | `fibermap.WithParams(StructType{})` + YAML `:name` segments |
| Header parameters | `fibermap.WithHeaders(StructType{})` — fields with `header:"X-Name"` tag |
| Response schemas | `fibermap.WithResponse(status, StructType{})` per-status |
| Security requirement | `MapMiddlewareToSecurity` matching the route's middleware list |
| Default responses | `WithDefaultResponse(status, StructType{})` |

Schema reflection uses Go struct tags: `json:"name"`, `validate:"required,min=1,max=200"`, `description:"..."`. `validate` rules translate to OpenAPI `required` / `minLength` / `maximum` / `enum` etc.

## Limitations

- **Schema reflection covers JSON tags + validator rules.** Custom JSON marshallers / interfaces don't reflect.
- **Discriminated unions** require manual `oneOf` schema overrides — not auto-derived.
- **No spec versioning** — `WithInfo.Version` is the OpenAPI doc version, not API contract version. Bump it on breaking changes manually.
- **Polymorphic responses** (different schema per status from the same handler) supported via multiple `WithResponse(status, model)` calls.
- **YAML route metadata is the source of truth.** `summary`/`description`/`tags` set in struct tags or godoc are NOT reflected — keep them in routes.yaml.
- **Scalar/Swagger/Redoc UI loads its CDN assets.** No internet at render time → blank UI. The JSON at `/openapi.json` still works.

## See also

- [`fibermap`](../README.md) — registers the handlers + metadata this package reflects
- [`errs`](../../errs/README.md) — `errs.Response{}` for default error response schemas
- [`examples/tasks`](../../examples/tasks/) — uses openapi for `/openapi.json` + `/docs` (Scalar UI)
- [`examples/urlshort`](../../examples/urlshort/README.md) — minimal openapi mount
