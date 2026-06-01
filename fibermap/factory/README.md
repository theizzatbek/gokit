# fibermap/factory

Готовые хелперы `MiddlewareFactoryFunc[T]` для самых частых параметризуемых через YAML middleware: `RequireRole`, `RequireAnyScope` и адаптеры, которые оборачивают сырые Fiber-хендлеры в форму fibermap factory.

**Родитель:** [../README.md](../README.md)
**Импорт:** `github.com/theizzatbek/gokit/fibermap/factory`

## Использование

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

Теперь в `routes.yaml`:

```yaml
middleware:
  - require_role: [admin]
  - require_scope: [orders:write, orders:admin]
```

## Заметки

- **Обе factory принимают опциональную `WithDenyHandler(h)`.** По умолчанию отказ возвращает `*errs.Error{Kind: Permission}` → 403; override для кастомной формы.
- **`Adapter[T](h fiber.Handler)`** поднимает сырой `*fiber.Ctx` хендлер в `MiddlewareFunc[T]`, который игнорирует `Context[T].Data` — полезно для plain Fiber middlewares (CORS, helmet, rate-limit).
- **`AdapterFactory[T](newFn)`** — factory-вариант: `newFn(args []string) (fiber.Handler, error)` производит сырой Fiber-хендлер на каждый YAML-вызов; адаптер заворачивает его в типизированную форму factory.
- **Пакет auth/fibermount использует `Adapter`/`AdapterFactory` внутри** для регистрации factory `bearer`/`require_scope`/`require_role` из `*auth.Auth[C]`. Используйте его вместо того, чтобы писать role/scope factory с нуля, если вы уже на `gokit/auth`.

## См. также

- [`fibermap`](../README.md) — контракт RegisterMiddlewareFactory
- [`auth/fibermount`](../../auth/fibermount/README.md) — turnkey bearer + require_scope + require_role mount для `*auth.Auth[C]`
</content>
