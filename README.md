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

## Run the example

A complete runnable demo lives in [`examples/quickstart`](./examples/quickstart):

```bash
cd examples/quickstart
go run .

# in another shell:
curl                 'http://localhost:3000/v1/patients'
curl -X POST         'http://localhost:3000/v1/patients?role=director'
curl -X POST         'http://localhost:3000/v1/patients?role=guest'      # 403
curl -X PUT          'http://localhost:3000/v1/patients/7?role=director'
```

The example prints the resolved route table at startup and uses a stub
auth middleware that takes the role from `?role=` so role guards are easy
to exercise from curl.

## Lifecycle

```
New → SetContextBuilder
    → RegisterHandler / RegisterMiddleware / RegisterMiddlewareFactory
    → LoadFile / LoadBytes / LoadFS
    → Validate                  (optional dry-run, no router needed)
    → Mount                     (one-shot; subsequent calls error)
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
| `name`           | string      | Free-form identifier; surfaced via `Routes()`.        |
| `tags`           | `[]string`  | Free-form; surfaced via `Routes()`.                   |
| `description`    | string      | Free-form; surfaced via `Routes()`.                   |

`name`, `tags`, and `description` are not interpreted — they exist for
introspection tooling (see below).

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

## Body binding & validation

Subpackage `fibermap/bind` ships a generic helper that combines
`BodyParser` with a validator pass — typical request entry-point
boilerplate, but typed and one-liner:

```go
import (
    "github.com/go-playground/validator/v10"
    "github.com/theizzatbek/fibermap/bind"
)

var v = validator.New()

type CreateTaskReq struct {
    Title string `json:"title" validate:"required,min=1,max=200"`
}

func (h *H) Create(c *Ctx) error {
    req, err := bind.Body[CreateTaskReq](c.Ctx, v)
    if err != nil {
        return c.Status(400).JSON(fiber.Map{"error": err.Error()})
    }
    // req is populated and validated; go.
    ...
}
```

The validator is injected via a one-method `Validator` interface
(`Struct(any) error`) — **fibermap does not depend on
`go-playground/validator`**. `*validator.Validate` satisfies the
interface as-is, but any custom validator (JSON Schema, hand-rolled,
...) works too. Pass `nil` to skip validation when you trust the
body shape.

Errors are wrapped via `errors.Is`: `bind.ErrParseBody` on JSON
failure, `bind.ErrValidateBody` on validation failure — so the
caller can distinguish them if it needs different HTTP responses.

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
