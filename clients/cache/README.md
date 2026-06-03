# cache

Типизированный Redis-backed read-through кеш. Generic над типом
значения T; даёт positive/negative caching с TTL knob'ами, prefix
namespacing, JSON-кодирование и best-effort обработку ошибок, чтобы
caller'ам никогда не приходилось защищаться от транзиентных Redis-сбоев.

**Импорт:** `github.com/theizzatbek/gokit/clients/cache`
**Зависит от:** `github.com/redis/go-redis/v9` (клиент предоставляется
caller'ом — `redis.UniversalClient`, обычно из `redisclient.Client.Universal()`;
работает с single-node, cluster, и sentinel)

## Quickstart

```go
type User struct {
    ID   string `json:"id"`
    Name string `json:"name"`
}

// Однострочник через cache.For — авто-подключает логгер из китового
// *redisclient.Client, возвращает nil когда svc.Redis nil
// (методы кеша nil-receiver-safe), паникует с *errs.Error при
// пустом KeyPrefix (программерская ошибка, fail-fast на старте).
c := cache.For[User](svc.Redis, "session:user:")
```

Или с полным контролем над TTL / codec / metrics / jitter:

```go
c, err := cache.New[User](svc.Redis.Universal(), cache.Config{
    KeyPrefix:   "session:user:",
    PositiveTTL: time.Hour,
    NegativeTTL: time.Minute,
    Logger:      svc.Logger(),
    Name:        "users",          // required when MetricsReg set
    MetricsReg:  reg,
    TTLJitter:   0.20,             // ±20% jitter against stampede
    // Codec:    msgpack.Codec{}, // optional override (JSON по умолчанию)
})
if err != nil { return err }

hit := c.Get(ctx, "u-42")
switch {
case hit.Value != nil:      // positive hit
case hit.NotFound:           // negative hit ("known bad")
default:                     // miss → fall through to DB
    u, err := db.LoadUser(ctx, "u-42")
    if errors.Is(err, sql.ErrNoRows) {
        c.SetNotFound(ctx, "u-42")
        return nil, NotFound
    }
    c.Set(ctx, "u-42", u)
}
```

## API

```go
func New[T any](rdb redis.UniversalClient, cfg Config) (*Redis[T], error)
func For[T any](rc *redisclient.Client, keyPrefix string) *Redis[T]

type Lookup[T any] struct {
    Value    *T
    NotFound bool
}

func (c *Redis[T]) Get(ctx, key)                     Lookup[T]
func (c *Redis[T]) Set(ctx, key, T)
func (c *Redis[T]) SetNotFound(ctx, key)
func (c *Redis[T]) Invalidate(ctx, key)
func (c *Redis[T]) InvalidatePrefix(ctx, partial)    // SCAN+DEL
func (c *Redis[T]) GetOrLoad(ctx, key, LoaderFn[T])  (Lookup[T], error)

type LoaderFn[T any] func(ctx, key) (T, bool, error)  // val, found, err

type Codec interface {
    Marshal(v any) ([]byte, error)
    Unmarshal(b []byte, v any) error
}
type JSONCodec struct{}  // default
```

`Lookup` — tri-state:

| `Value` | `NotFound` | Смысл |
|---|---|---|
| non-nil | false | positive hit; используйте Value |
| nil | true | negative hit; считайте not-found без обращения к источнику |
| nil | false | miss; query источник |

## Config

| Поле | По умолчанию | Заметки |
|---|---|---|
| `KeyPrefix` | — | Обязательно. Хранимые ключи — `KeyPrefix + key`. Namespace per value type И per service при шаринге Redis-инстанса. |
| `PositiveTTL` | 1h | Positive-записи протухают через это время. |
| `NegativeTTL` | 60s | TTL negative-cache sentinel'а. 0 → default 60s; явно установите очень малое значение, чтобы эффективно отключить. |
| `Logger` | nil (silent) | Получает Warn-записи на Redis-transport или encode/decode failures. |
| `Name` | "" | Метка кэш-инстанса в metrics. Обязательно когда `MetricsReg` установлен. |
| `MetricsReg` | nil | Регистрирует `cache_operations_total{name,operation,outcome}` + `cache_operation_duration_seconds{name,operation}`. |
| `TTLJitter` | 0 (no jitter) | Фракция (0..1) равномерного ±шума на TTL'ы. Защищает popular keys от sync-expiry storm. Типично 0.10–0.25. |
| `Codec` | `JSONCodec{}` | Custom serialisation. Pluggable msgpack/protobuf/etc. |

## Политика best-effort ошибок

Каждая Redis-side ошибка **логируется + проглатывается**:

- `Get` на transport error → miss. Caller проваливается к источнику.
- `Set` / `SetNotFound` / `Invalidate` на transport error → log + return. Source of truth не меняется.
- JSON encode/decode failures → log + miss (Get) или noop (Set).

Это намеренно. Кеш, который пропагирует ошибки, заставляет каждое
вызывающее место уходить в defensive double-path; обработка
транзиентного Redis-hiccup как miss держит код caller'ов линейным.

## Read-through через `GetOrLoad`

`GetOrLoad` — read-through helper с встроенным single-flight против cache stampede:

```go
hit, err := c.GetOrLoad(ctx, "u-42",
    func(ctx context.Context, key string) (User, bool, error) {
        u, err := db.LoadUser(ctx, key)
        if errors.Is(err, sql.ErrNoRows) {
            return User{}, false, nil // → SetNotFound, negative cache
        }
        if err != nil {
            return User{}, false, err // → НЕ кешируется; surface to caller
        }
        return u, true, nil           // → Set + positive hit
    })
```

- N concurrent goroutines на тот же key → loader вызывается **один раз**; остальные ждут результат.
- Loader err НЕ poison'ит cache — следующий вызов retry'ит.

## InvalidatePrefix (массовый сброс)

Когда нужно дропнуть всю группу keys (`tenant-a:*`):

```go
c.InvalidatePrefix(ctx, "tenant-a:")
```

Использует `SCAN + DEL` pipelined — не блокирует Redis как `KEYS`. Cluster-mode caveat: `SCAN` идёт per-shard; для full-coverage в cluster пинуйте все keys через hashtag (`{tenant-a}:user:42`) — тогда всё в одном слоте.

## Cluster / Sentinel

`*Redis[T]` обёртка над `redis.UniversalClient` — поэтому `cache.For[T](svc.Redis, ...)` работает одинаково для single-node, cluster, и sentinel deployments:

```go
// service.Config.Redis.URL=redis://... → svc.Redis.Mode()==Single
// в будущем кит может выставить ClusterConfig — cache следит за изменением через svc.Redis.Universal().
```

## Negative caching

`SetNotFound(ctx, key)` сохраняет крошечный sentinel под ключом, так что
следующий `Get` возвращает `Lookup{NotFound: true}` без проверки источника
правды. Killer-фича для 404-поглощения scanner-трафика на публичных
эндпоинтах — сочетайте с route-level rate limiting и коротким
`NegativeTTL` (60s default), чтобы более поздний `Create` сработал
внутри окна.

## nil-receiver safety

`(*Redis[T])(nil)` безопасен на каждом методе:

- `Get` возвращает zero `Lookup{}` (miss).
- `Set` / `SetNotFound` / `Invalidate` — no-op.

Позволяет пробрасывать cache-reference через сервисы безусловно;
путь "cache off" — это "не конструируйте и передайте nil".

## См. также

- [`clients/redis`](../redis/README.md) — kit-thin Redis-клиент обёртка, которая производит `*redis.Client`, потребляемый этим пакетом.
- [`service`](../../service/README.md) — `service.Config.Redis` + `svc.Redis` авто-проводка.
</content>
