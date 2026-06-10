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

## Admin / operator API

Методы вне `auth.RefreshStore`-интерфейса, доступные на `*refreshredis.Store` напрямую:

| Метод | Возвращает | Заметки |
|---|---|---|
| `ListBySubject(ctx, subject) ([]SessionInfo, error)` | sessions ordered `issued_at DESC` | Backed by `refresh:subject:{subject}` set + pipelined `HGETALL`. Член set'а с уже EXPIREATd хешем silently пропускается. |
| `Stats(ctx) (StoreStats, error)` | `{Active, Consumed, Revoked, Expired, Total}` | O(N) — `SCAN refresh:*` (исключая aux-сеты) + pipelined `HMGET`. Для admin / diagnostic, не hot path. EXPIREATd токены НЕ видны (Redis их уже выгрузил). Bound через `WithStatsCap(N)`: при превышении возвращается `ErrStatsCapExceeded` (sentinel — `errors.Is(err, refreshredis.ErrStatsCapExceeded)`), partial counts отбрасываются. |
| `RevokeByIP(ctx, ip) (int64, error)` | число revoked tokens | Backed by вспомогательным set'ом `refresh:ip:{ip}`, заполняемым при Issue'е (когда `r.IP != ""`). Старые токены до этого фичи не индексированы — для retroactive sweep пройдитесь Stats/ListBySubject. |

`SessionInfo` — admin projection без `token_hash`. `ConsumedAt`/`RevokedAt` — sentinel: nil-zero = "консьюмнут / revoked флаг выставлен" (Redis-store хранит только булевы флаги, не точные timestamps). Поле `State` — `"active" | "consumed" | "revoked" | "expired"`.

## Observability + хуки

`refreshredis.New(rdb, opts...)` принимает функциональные опции (обратно совместимо с `refreshredis.New(rdb)`):

- `WithMetrics(reg prometheus.Registerer)` → `refreshredis_ops_total{op,outcome}` + `refreshredis_op_duration_seconds{op}` (op: `issue|consume|revoke_family|revoke_subject|revoke_ip|gc|stats|list`; outcome: `ok|error`; consume также `missing|expired|reused`).
- `WithLogger(*slog.Logger)` — silent по умолчанию; для panic-recovery в hooks.
- `WithOnConsumeReused(fn)` — fires внутри `Consume` после reuse-detection (Lua-скрипт уже revoke'нул family). **OAuth 2.1 stolen-token alert** — подключите к SIEM.
- `WithOnFamilyRevoke(fn)` / `WithOnSubjectRevoke(fn)` / `WithOnIPRevoke(fn)` — post-revoke audit hooks. Panic-safe.

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
