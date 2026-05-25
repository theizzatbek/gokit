# fibermap/factory

Pre-built `MiddlewareFactoryFunc[T]` helpers for the common YAML-parameterised middlewares: `RequireRole`, `RequireAnyScope`, and adapters that wrap raw Fiber handlers into the fibermap factory shape.

**Parent:** [../README.md](../README.md)
**Import:** `github.com/theizzatbek/gokit/fibermap/factory`

## Use

```go
import (
    "github.com/theizzatbek/gokit/fibermap"
    "github.com/theizzatbek/gokit/fibermap/factory"
)

type AppCtx struct {
    Role   string
    Scopes []string
}

fibermap.RegisterMiddlewareFactory(eng, "require_role",
    factory.RequireRole[AppCtx](
        func(c *fibermap.Context[AppCtx]) string { return c.Data.Role },
    ))

fibermap.RegisterMiddlewareFactory(eng, "require_scope",
    factory.RequireAnyScope[AppCtx](
        func(c *fibermap.Context[AppCtx]) []string { return c.Data.Scopes },
    ))
```

Now in `routes.yaml`:

```yaml
middleware:
  - require_role: [admin]
  - require_scope: [orders:write, orders:admin]
```

## Notes

- **Both factories accept opt-in `WithDenyHandler(h)`.** By default a deny returns `*errs.Error{Kind: Permission}` → 403; override for custom shapes.
- **`Adapter[T](h fiber.Handler)`** lifts a raw `*fiber.Ctx` handler into a `MiddlewareFunc[T]` that ignores `Context[T].Data` — useful for plain Fiber middlewares (CORS, helmet, rate-limit).
- **`AdapterFactory[T](newFn)`** is the factory variant — `newFn(args []string) (fiber.Handler, error)` produces a raw Fiber handler per YAML invocation; the adapter wraps it into the typed factory shape.
- **The auth/fibermount package uses `Adapter`/`AdapterFactory` internally** to register `bearer`/`require_scope`/`require_role` factories from `*auth.Auth[C]`. Use that instead of writing role/scope factories from scratch if you're already using `gokit/auth`.

## See also

- [`fibermap`](../README.md) — RegisterMiddlewareFactory contract
- [`auth/fibermount`](../../auth/fibermount/README.md) — turnkey bearer + require_scope + require_role mount for `*auth.Auth[C]`
