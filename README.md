# fibermap

YAML-declarative router and middleware composer for [Fiber](https://github.com/gofiber/fiber).

- Describe your route tree in YAML.
- Register handlers and middleware by name (no reflection).
- Get a typed per-request context.

Status: **0.x — API unstable.**

## Install

```bash
go get github.com/theizzatbek/fibermap
```

Requires Go 1.23+ and Fiber v2.

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
    → RegisterHandler / RegisterMiddleware
    → SetRoleChecker            (required iff any route declares roles:)
    → LoadFile / LoadBytes
    → Mount                     (one-shot; subsequent calls error)
```

`Mount` validates everything against registered names and returns *all*
problems at once via `errors.Join`. No routes are installed if validation
fails.

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
eng.RegisterMiddlewareFactory("require_role",
    func(args []string) (fibermap.MiddlewareFunc[AppCtx], error) {
        allowed := append([]string(nil), args...)
        return func(ctx *fibermap.Context[AppCtx]) error {
            for _, r := range allowed {
                if r == ctx.Data.Role { return ctx.Next() }
            }
            return ctx.Status(403).JSON(fiber.Map{"error": "forbidden"})
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

Handlers receive the typed context:

```go
func (h *Patient) Create(ctx *fibermap.Context[AppCtx]) error {
    // ctx.Data.UserID is already populated by ContextBuilder
    // ctx.Status / ctx.JSON / etc. — all Fiber methods via embedding
    return ctx.Status(201).JSON(...)
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
eng.RegisterMiddlewareFactory("require_role",
    func(args []string) (fibermap.MiddlewareFunc[AppCtx], error) {
        if len(args) == 0 {
            return nil, errors.New("require_role: at least one role required")
        }
        allowed := append([]string(nil), args...)
        return func(c *fibermap.Context[AppCtx]) error {
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
calls). The returned slice and each entry's slice fields are independent
copies — mutating them does not affect engine state. Useful for generating
OpenAPI/docs or printing a route table at boot.

## Error handling

- Register-time (programmer error, before mount): a duplicate name within
  or across the plain/factory registries panics with `*Error` /
  `CodeDuplicateRegistration`. There is no return value to check —
  registration follows the `MustCompile` convention.
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
