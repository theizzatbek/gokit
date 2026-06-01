# cache

Типизированный Redis-backed read-through кеш. Generic над типом
значения T; даёт positive/negative caching с TTL knob'ами, prefix
namespacing, JSON-кодирование и best-effort обработку ошибок, чтобы
caller'ам никогда не приходилось защищаться от транзиентных Redis-сбоев.

**Импорт:** `github.com/theizzatbek/gokit/clients/cache`
**Зависит от:** `github.com/redis/go-redis/v9` (сырой клиент
предоставляется caller'ом — обычно из `redisclient.Client.Redis()`)

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

Или с полным контролем над TTL / кастомным логгером / сырым
`*redis.Client`:

```go
c, err := cache.New[User](svc.Redis.Redis(), cache.Config{
    KeyPrefix:   "session:user:",
    PositiveTTL: time.Hour,
    NegativeTTL: time.Minute,
    Logger:      svc.Logger(),
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
func New[T any](rdb *redis.Client, cfg Config) (*Redis[T], error)

type Lookup[T any] struct {
    Value    *T
    NotFound bool
}

func (c *Redis[T]) Get(ctx, key)        Lookup[T]
func (c *Redis[T]) Set(ctx, key, T)
func (c *Redis[T]) SetNotFound(ctx, key)
func (c *Redis[T]) Invalidate(ctx, key)
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

## Политика best-effort ошибок

Каждая Redis-side ошибка **логируется + проглатывается**:

- `Get` на transport error → miss. Caller проваливается к источнику.
- `Set` / `SetNotFound` / `Invalidate` на transport error → log + return. Source of truth не меняется.
- JSON encode/decode failures → log + miss (Get) или noop (Set).

Это намеренно. Кеш, который пропагирует ошибки, заставляет каждое
вызывающее место уходить в defensive double-path; обработка
транзиентного Redis-hiccup как miss держит код caller'ов линейным.

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
