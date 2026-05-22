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
eng.SetRoleChecker(func(ctx *fibermap.Context[AppCtx], allowed []string) bool {
    for _, r := range allowed {
        if r == ctx.Data.Role { return true }
    }
    return false
})

_ = eng.RegisterMiddleware("auth", authMW)
_ = eng.RegisterMiddleware("audit", auditMW)
_ = eng.RegisterHandler("patient.create", patient.Create)

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
          - { method: POST, path: "",    handler: patient.create, roles: [director, receptionist] }
          - { method: PUT,  path: /:id,  handler: patient.update, roles: [director], middleware: [audit] }
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
| `middleware_sets` | `map[string][]string` | Named bundles of middleware names. May reference other set names; recursively expanded. |
| `groups`          | `[]Group`           | Route tree.                                    |

Group:

| Field            | Type        | Notes                                                 |
| ---------------- | ----------- | ----------------------------------------------------- |
| `prefix`         | string      | Appended to ancestor prefix.                          |
| `middleware`     | `[]string`  | Concrete middleware names (registered via `RegisterMiddleware`). |
| `middleware_set` | string      | Name from `middleware_sets`. Validated at mount.      |
| `routes`         | `[]Route`   |                                                       |
| `groups`         | `[]Group`   | Nested groups inherit prefix + middleware.            |

Route:

| Field            | Type        | Notes                                                 |
| ---------------- | ----------- | ----------------------------------------------------- |
| `method`         | string      | Required. `GET`/`POST`/`PUT`/`PATCH`/`DELETE`/`HEAD`/`OPTIONS`. |
| `path`           | string      | Fiber path pattern (`/:id`, wildcards, etc).          |
| `handler`        | string      | Required. Name registered via `RegisterHandler`.      |
| `middleware`     | `[]string`  | Appended after ancestor chain.                        |
| `middleware_set` | string      |                                                       |
| `roles`          | `[]string`  | Triggers `RoleChecker` after the middleware chain.    |
| `name`           | string      | Free-form identifier; surfaced via `Routes()`.        |
| `tags`           | `[]string`  | Free-form; surfaced via `Routes()`.                   |
| `description`    | string      | Free-form; surfaced via `Routes()`.                   |

`name`, `tags`, and `description` are not interpreted — they exist for
introspection tooling (see below).

## Middleware sets

A set is just a named list of middleware names. Sets may reference other set
names; resolution is recursive. The final chain for a route is:

```
outermost ancestor group → … → route's own middleware → role guard (if roles:)
```

Duplicates are dropped, keeping the first occurrence. Cycles between set
names are detected at parse time (`CodeMiddlewareCycle`); a reference to an
undefined set name fails at mount time (`CodeUnknownMiddlewareSet`).

## Introspection

After `Mount`, `Engine.Routes()` returns a snapshot of every installed route:

```go
for _, r := range eng.Routes() {
    fmt.Printf("%-6s %-30s -> %s  roles=%v middleware=%v\n",
        r.Method, r.Path, r.Handler, r.Roles, r.Middleware)
}
```

`RouteInfo` carries `Method`, `Path`, `Handler`, `Name`, `Description`,
`Middleware` (resolved chain, sentinel filtered out), `Roles`, and `Tags`.
The returned slice and each entry's slice fields are independent copies —
mutating them does not affect engine state. Useful for generating
OpenAPI/docs or printing a route table at boot.

## Error handling

- Parse-time errors (bad YAML, missing fields, invalid HTTP method, cycles in
  `middleware_sets`) — returned by `LoadFile`/`LoadBytes`.
- Mount-time errors (unknown handler/middleware name, missing role checker,
  duplicate `method+path`, no `ContextBuilder`) — accumulated and returned via
  `errors.Join` from `Mount`.
- Runtime: a failing `ContextBuilder` triggers `SetContextErrorHandler`
  (default 500). A failing `RoleChecker` triggers `SetForbiddenHandler`
  (default 403). Handler-returned errors pass through to Fiber's normal
  `ErrorHandler`.

## License

MIT. See `LICENSE`.
