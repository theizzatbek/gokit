# auth/refreshredis

Redis-backed `auth.RefreshStore` поверх `redis/go-redis/v9`. Каждая запись — это один HASH с `EXPIREAT`; family + subject SET'ы обеспечивают bulk-revoke пути. `Consume` выполняется как один Lua-скрипт для атомарности (consume + reuse detection + family revoke — всё server-side).

**Родитель:** [../README.md](../README.md)
**Импорт:** `github.com/theizzatbek/gokit/auth/refreshredis`

## Использование

```go
import (
    "github.com/redis/go-redis/v9"
    "github.com/theizzatbek/gokit/auth"
    "github.com/theizzatbek/gokit/auth/refreshredis"
)

rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

authObj, _ := auth.New[MyClaims](auth.Config{
    Issuer: "myservice", Keys: ks, AccessTTL: 15*time.Minute, RefreshTTL: 30*24*time.Hour,
}, auth.WithRefreshStore(refreshredis.New(rdb)))
```

## Заметки

- **Тот же контракт, что и у [`refreshpg`](../refreshpg/README.md).** Выбирайте Redis, когда не хочется расширять Postgres-схему, или нужен sub-ms Consume.
- **Авто-expiration через `EXPIREAT`.** Истёкшие токены GC'ятся сами — periodic cleanup job не нужен.
- **Один Lua-скрипт на Consume.** Устраняет round-trip race window между SELECT + UPDATE. Корректно реплицируется через семантику Redis MULTI.
- **Family + subject SET'ы** индексируют записи для `RevokeFamily` и `RevokeAllForSubject` без сканирования. SET'ы авто-EXPIRE'ятся вместе с самым долгоживущим членом family.
- **Только хеши токенов.** Та же security property, что и у refreshpg — leak DB/кэша не компрометирует сырые токены.
- **Cluster-safe**, когда ключи для данной family хешатся в один слот — Redis обрабатывает это через hash-tags, встроенные в схему именования ключей.

## Выбор между refreshpg и refreshredis

| Критерий | refreshpg | refreshredis |
|---|---|---|
| Единый source of truth | ✓ (в той же DB, что и users) | ✗ (отдельная persistence) |
| Sub-ms Consume latency | средняя | ✓ |
| Авто-expiration | ✗ (ручная чистка) | ✓ |
| Failure-domain | общий с app DB | независимый |
| Операционный overhead | без дополнительного | Redis-инстанс |

Большинство сервисов начинают с `refreshpg` и мигрируют на `refreshredis` только если refresh latency становится hotspot'ом, или они разделяют short-lived state и durable data.

## Тестирование

Используйте [testcontainers-go/modules/redis](https://golang.testcontainers.org/modules/redis/):

```go
c, _ := tcredis.Run(ctx, "redis:7-alpine")
defer testcontainers.TerminateContainer(c)

endpoint, _ := c.Endpoint(ctx, "")
rdb := redis.NewClient(&redis.Options{Addr: endpoint})
store := refreshredis.New(rdb)
```

## См. также

- [`auth`](../README.md) — родитель
- [`auth/refreshpg`](../refreshpg/README.md) — Postgres-backed альтернатива с идентичным контрактом
</content>
