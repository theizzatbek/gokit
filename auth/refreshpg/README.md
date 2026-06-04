# auth/refreshpg

Postgres-backed `auth.RefreshStore` поверх `db.Querier`. Атомарный `Consume` через единый `UPDATE … RETURNING`; reuse detection триггерит family-wide `RevokeFamily` перед возвратом `*errs.Error{Code: "refresh_reused"}`. DDL живёт в `schema.sql` — сам пакет миграции не выполняет.

**Родитель:** [../README.md](../README.md)
**Импорт:** `github.com/theizzatbek/gokit/auth/refreshpg`

## Использование

```go
import (
    "github.com/theizzatbek/gokit/auth"
    "github.com/theizzatbek/gokit/auth/refreshpg"
    "github.com/theizzatbek/gokit/db"
)

d, _ := db.Connect(ctx, dbCfg)

authObj, _ := auth.New[MyClaims](auth.Config{
    Issuer: "myservice", Keys: ks, AccessTTL: 15*time.Minute, RefreshTTL: 30*24*time.Hour,
}, auth.WithRefreshStore(refreshpg.New(d)))
```

`refreshpg.New(d)` принимает любой `db.Querier` — `*db.DB` или `*db.Tx`. Тесты могут передать транзакцию, чтобы изменения откатывались на конце теста.

## Схема

Примените `schema.sql` (или скопируйте DDL в свою миграцию) перед первым использованием:

```sql
CREATE TABLE IF NOT EXISTS auth_refresh_tokens (
    token_hash  bytea       PRIMARY KEY,
    family_id   uuid        NOT NULL,
    parent_hash bytea       NOT NULL,
    subject     text        NOT NULL,
    issued_at   timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    revoked_at  timestamptz,
    user_agent  text        NOT NULL DEFAULT '',
    ip          inet
);
CREATE INDEX IF NOT EXISTS auth_refresh_tokens_family_id_idx ON auth_refresh_tokens (family_id);
CREATE INDEX IF NOT EXISTS auth_refresh_tokens_subject_idx   ON auth_refresh_tokens (subject);
CREATE INDEX IF NOT EXISTS auth_refresh_tokens_expires_at_idx ON auth_refresh_tokens (expires_at);
```

`examples/urlshort/migrations/0001_init.sql` включает этот DDL дословно рядом с собственными таблицами сервиса.

## Admin / operator API

`*Store` несёт ряд методов вне `auth.RefreshStore`-интерфейса — для admin-эндпоинтов, инцидент-респонса и cron-cleanup'а. Доступны на типе `*refreshpg.Store` напрямую.

| Метод | Возвращает | Заметки |
|---|---|---|
| `ListBySubject(ctx, subject) ([]SessionInfo, error)` | history sessions | Все строки subject ordered `issued_at DESC`; включает active / consumed / revoked / expired (UI фильтрует по `State`). |
| `Stats(ctx) (StoreStats, error)` | `{Active, Consumed, Revoked, Expired, Total}` | Disjoint buckets, один round trip. |
| `RevokeByIP(ctx, ip) (int64, error)` | число revoked tokens | Bulk-revoke по IP-адресу для incident response. Empty ip → 0; unknown ip → 0 (idempotent). |
| `GarbageCollectBatch(ctx, now, limit, maxIterations)` | число удалённых | Chunked variant of `GarbageCollect` для очень больших таблиц; loop `DELETE … LIMIT N` пока не вернёт 0. `limit ≤ 0` → 1000, `maxIterations ≤ 0` → 1024. На ctx cancel возвращает частичный прогресс. |

`SessionInfo` ничего не несёт из `token_hash` — секрет никогда не покидает store. Поле `State` — `"active" | "consumed" | "revoked" | "expired"` (disjoint).

## Observability + хуки

`refreshpg.New(d, opts...)` принимает функциональные опции (обратно совместимо с `refreshpg.New(d)`):

- `WithMetrics(reg prometheus.Registerer)` регистрирует:
  - `refreshpg_ops_total{op,outcome}` — Issue / Consume / RevokeFamily / RevokeSubject / RevokeByIP / GC / Stats / List; outcome — `ok | error` (consume также: `missing | expired | reused`).
  - `refreshpg_op_duration_seconds{op}` — histogram wall-clock latency.
- `WithLogger(*slog.Logger)` — silent по умолчанию; используется только для panic-recovery в hooks и диагностических warning'ов.
- `WithOnConsumeReused(fn)` — fires ВНУТРИ `Consume` после reuse-detection (`RevokeFamily` уже отработал). Подключите к SIEM / Sentry — это **OAuth 2.1 stolen-token alert**.
- `WithOnFamilyRevoke(fn)` / `WithOnSubjectRevoke(fn)` / `WithOnIPRevoke(fn)` — post-revoke audit hooks. Все panic-safe (recovered + WARN-logged через `WithLogger`).

## Заметки

- **Хеши токенов, а не сами токены.** Сырой refresh token никогда не попадает в БД — только `sha256(token)`. Утечка БД не компрометирует активные refresh-токены.
- **Family revoke при reuse.** Когда `Consume` видит токен, у которого `consumed_at IS NOT NULL`, он делает `RevokeFamily(family_id)` перед возвратом ошибки. Это каноническая реакция на "stolen-token detected": invalidate каждого потомка скомпрометированного root-токена.
- **Никакой фоновой чистки expired.** Истёкшие строки остаются в таблице. Запускайте `GarbageCollect(ctx, now)` (одним DELETE) или `GarbageCollectBatch(ctx, now, 1000, 0)` (chunked) из nightly cron'а, если хотите освобождать место.
- **Атомарно через `UPDATE … RETURNING`.** Никакого race window SELECT-then-UPDATE. Диагностический `SELECT` на miss-пути классифицирует, существовал ли токен вообще или уже был consumed.
- **`SecurityLogger`** на `*auth.Auth` (через `auth.WithSecurityLogger`) эмитит структурированные WARN-события для reuse-triggered revocations — подключите к вашему SIEM/alerting.

## Тестирование

Используйте [testcontainers-go/modules/postgres](https://golang.testcontainers.org/modules/postgres/). Паттерн из `store_test.go` самого gokit:

```go
ctx := context.Background()
c, _ := tcpostgres.Run(ctx, "postgres:16-alpine",
    tcpostgres.WithDatabase("test"), tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
    tcpostgres.BasicWaitStrategies())
defer testcontainers.TerminateContainer(c)

d, _ := db.Connect(ctx, /* derive cfg from c */)
_, _ = d.Exec(ctx, refreshpg.SchemaSQL())  // или inline DDL
store := refreshpg.New(d)
```

## См. также

- [`auth`](../README.md) — родитель: `WithRefreshStore` потребляет это
- [`auth/refreshredis`](../refreshredis/README.md) — тот же контракт, Redis-backed
- [`db`](../../db/README.md) — предоставляет интерфейс `Querier`
</content>
