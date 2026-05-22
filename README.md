# fibermap

YAML-declarative router and middleware composer for [Fiber](https://github.com/gofiber/fiber).

- Describe your route tree in YAML.
- Register handlers and middleware by name (no reflection).
- Get a typed per-request context.

Status: **0.x — API unstable.**

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
