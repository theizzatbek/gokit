# fibermap

YAML-declarative router and middleware composer for [Fiber](https://github.com/gofiber/fiber). Describe your route tree in YAML, register handlers by name, get a typed per-request context.

Status: **0.x — API unstable.**

## Quick example

See `engine_test.go` for runnable examples.

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
    return slices.Contains(allowed, ctx.Data.Role)
})

_ = eng.RegisterMiddleware("audit_log", auditLog)
_ = eng.RegisterHandler("patient.create", h.Create)

if err := eng.LoadFile("routes.yaml"); err != nil { panic(err) }
if err := eng.Mount(app); err != nil { panic(err) }
```

## License

MIT.