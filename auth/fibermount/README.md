# auth/fibermount

One-call мост между `*auth.Auth[C]` и `*fibermap.Engine[T]`. Регистрирует factory middleware `bearer`, `require_scope` и `require_role` на engine, чтобы routes.yaml мог использовать их по имени. Мост живёт в подпакете, чтобы core `auth/` оставался framework-agnostic (никакого импорта fibermap там).

**Родитель:** [../README.md](../README.md)
**Импорт:** `github.com/theizzatbek/gokit/auth/fibermount`

**Внутри этого пакета:** core-wiring (`bearer`/`require_scope`/`require_role`/`require_any_scope`/`require_any_role`/`rate_limit`/`idempotency`/`api_key`) фактически живёт в [`auth/authmount`](../authmount/README.md) — этот пакет делегирует туда для обратной совместимости и добавляет ровно два фактора сверху: `rate_limit_redis` (`ratelimit_redis.go`) и `idempotency_key`. Оба тянут `clients/ratelimit` → `clients/redis`, и Go резолвит зависимости попакетно — значит любой каллер, импортирующий `fibermount` хотя бы ради `MountMiddlewareFactories`, тащит Redis в свой граф вместе с этими двумя файлами, даже если ни разу их не вызывает. Если вам не нужны `rate_limit_redis`/`idempotency_key` — импортируйте `auth/authmount` напрямую и держите `go-redis` вне бинаря.

## Использование

```go
import (
    "github.com/theizzatbek/gokit/auth"
    "github.com/theizzatbek/gokit/auth/fibermount"
    "github.com/theizzatbek/gokit/fibermap"
)

eng := fibermap.New[AppCtx]()
authObj, _ := auth.New[MyClaims](cfg, auth.WithRefreshStore(store))

// Одна строка — регистрирует все три factory middleware.
if err := fibermount.MountMiddlewareFactories(eng, authObj); err != nil {
    return err
}
```

Теперь в `routes.yaml`:

```yaml
groups:
  - prefix: /links
    middleware:
      - bearer: []
    routes:
      - method: GET
        path: ""
        handler: links.list
      - method: DELETE
        path: /:code
        handler: links.delete
        middleware:
          - require_role: [admin]
```

И `bearer` (с `[]` = `BearerRequired`, `["optional"]` = `BearerOptional`), и `require_role`/`require_scope` (с arg-list'ами) теперь применимы как YAML factory middleware.

## Заметки

- **`MountMiddlewareFactories` — единственная публичная функция** — делает все три регистрации за раз. Если нужны только некоторые из них, регистрируйте отдельные методы `*Factory` у `*auth.Auth[C]` через `fibermap.RegisterMiddlewareFactory` руками.
- **Bearer на уровне fiber.App vs. per-route:** когда `ContextBuilder` вашего engine читает Bearer-principal (типично), auth-проверка должна выполняться ДО `contextInit`. Factory `bearer: []` из fibermount устанавливает per-route middleware, который запускается ПОСЛЕ `contextInit` — слишком поздно для builder'а. Решение: установите `authObj.Bearer(auth.BearerOptional)` на fiber.App через `fibermap.WithUse(...)`, чтобы principal был в Locals до того, как запустится builder; per-route `bearer: []` потом enforces 401 на защищённых путях.
- **Сам `auth/` НЕ импортирует `gokit/fibermap`.** Только этот мост это делает. Это сохраняет `auth` пригодным из не-Fiber кода (CLI, workers, скрипты).
- **Адаптирует через `factory.Adapter/AdapterFactory`** под капотом (см. [`fibermap/factory`](../../fibermap/factory/README.md)).

## См. также

- [`auth`](../README.md) — родитель: предоставляет методы `Bearer`/`RequireScopeFactory`/`RequireRoleFactory`, которые этот мост оборачивает
- [`auth/authmount`](../authmount/README.md) — Redis-свободное подмножество этого пакета; берите его напрямую, если `rate_limit_redis`/`idempotency_key` не нужны
- [`fibermap`](../../fibermap/README.md) — `RegisterMiddlewareFactory`, `WithUse`
- [`examples/urlshort`](../../examples/urlshort/README.md) — использует fibermount end-to-end
</content>
