# auth/authmount

Redis-свободная половина `auth/fibermount`. Регистрирует factory middleware `bearer`, `require_scope`, `require_role`, `require_any_scope`, `require_any_role`, `rate_limit`, `idempotency` и `api_key` на `*fibermap.Engine[T]`, привязанные к `*auth.Auth[C]`.

**Родитель:** [`auth`](../README.md)
**Импорт:** `github.com/theizzatbek/gokit/auth/authmount`

## Зачем этот пакет существует отдельно от `fibermount`

Go резолвит зависимости попакетно: если хотя бы один файл пакета импортирует что-то, любой каллер, импортирующий пакет ради *любой* его функции, тянет это что-то целиком — даже если реально вызывает только соседнюю функцию из того же пакета.

`auth/fibermount` — тот самый пакет с проблемой: рядом с `mount.go` (то, что нужно почти всем) там лежит `ratelimit_redis.go` (`MountRateLimitRedisFactory`), который импортирует `clients/ratelimit` → `clients/redis`. Каллер, которому нужен только `fibermount.MountMiddlewareFactories`, всё равно получает `go-redis` в графе зависимостей своего пакета — просто потому что он лежит в той же папке.

Для `svckit`, ядро которого по контракту не имеет права тянуть `clients/redis` (см. `TestCoreDoesNotImportOptionalSubsystems` в `svckit/`), это не мелочь: `mountAuthMiddleware` в `svckit/build_core.go` должен регистрировать core-факторки auth (`bearer`, `require_role`, …), не утаскивая Redis всем, кто просто хочет собрать сервис с Auth, но без rate-limit'а.

`authmount` — решение: он держит ровно core-wiring (`auth`, `fibermap`, `fiber` — и больше ничего) в отдельном пакете, так что импорт `authmount` не тащит `clients/ratelimit`/`clients/redis` вообще. `auth/fibermount.MountMiddlewareFactories` и `MountAPIKeyFactory` делегируют сюда для обратной совместимости — существующий код, импортирующий `fibermount`, компилируется без изменений.

## Использование

```go
import (
    "github.com/theizzatbek/gokit/auth"
    "github.com/theizzatbek/gokit/auth/authmount"
    "github.com/theizzatbek/gokit/fibermap"
)

eng := fibermap.New[AppCtx]()
authObj, _ := auth.New[MyClaims](cfg, auth.WithRefreshStore(store))

if err := authmount.MountMiddlewareFactories(eng, authObj); err != nil {
    return err
}
```

`routes.yaml` использует зарегистрированные имена как обычно:

```yaml
groups:
  - prefix: /links
    middleware:
      - bearer: []
    routes:
      - method: DELETE
        path: /:code
        handler: links.delete
        middleware:
          - require_role: [admin]
```

`api_key` регистрируется отдельным вызовом, потому что `KeyStore` — внешняя зависимость, а не побочный эффект конструирования `Auth`:

```go
if err := authmount.MountAPIKeyFactory(eng, authObj, keyStore); err != nil {
    return err
}
```

```yaml
middleware:
  - api_key: []            # обязательный ключ
  - api_key: ["optional"]   # анонимный fallback
```

## Когда брать `authmount`, когда `fibermount`

| Нужно | Берите |
|---|---|
| Только `bearer`/`require_scope`/`require_role`/`api_key` | `authmount` напрямую — без `clients/ratelimit`/`clients/redis` в графе |
| `rate_limit_redis` (`MountRateLimitRedisFactory`) или `idempotency_key` (`MountIdempotencyKeyFactory`) | `auth/fibermount` — эти два фактора живут только там |
| Пишете мод для `svckit` | `authmount` — ядро само использует его в `mountAuthMiddleware`, тот же принцип применяется к любому моду, для которого лишний транзитивный импорт стоит мегабайт |

## Заметки

- **Bearer на уровне fiber.App vs. per-route:** когда `ContextBuilder` вашего engine читает Bearer-principal (типично), auth-проверка должна выполняться ДО `contextInit`. Factory `bearer: []` устанавливает per-route middleware, который запускается ПОСЛЕ `contextInit` — слишком поздно для builder'а. Решение: установите `authObj.Bearer(auth.BearerOptional)` на fiber.App через `fibermap.WithUse(...)`, чтобы principal был в Locals до того, как запустится builder; per-route `bearer: []` потом enforces 401 на защищённых путях. `svckit.Run` уже делает это автоматически.
- **Сам `auth/` НЕ импортирует `gokit/fibermap`.** Только мосты (`authmount`, `fibermount`) это делают — `auth` остаётся пригодным из не-Fiber кода (CLI, workers, скрипты).
- **`MountMiddlewareFactories` / `MountAPIKeyFactory` — единственные публичные функции.** Нужны только некоторые факторки — регистрируйте отдельные методы `*Factory` у `*auth.Auth[C]` через `fibermap.RegisterMiddlewareFactory` руками.

## См. также

- [`auth`](../README.md) — родитель: предоставляет методы `Bearer`/`RequireScopeFactory`/`RequireRoleFactory`/…, которые этот мост оборачивает
- [`auth/fibermount`](../fibermount/README.md) — надмножество с `rate_limit_redis` и `idempotency_key`, платящее за Redis в графе зависимостей
- [`fibermap`](../../fibermap/README.md) — `RegisterMiddlewareFactory`, `WithUse`
- [`svckit`](../../svckit/README.md) — единственный каллер `authmount` вместо `fibermount` внутри кита
