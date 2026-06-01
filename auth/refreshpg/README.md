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

## Заметки

- **Хеши токенов, а не сами токены.** Сырой refresh token никогда не попадает в БД — только `sha256(token)`. Утечка БД не компрометирует активные refresh-токены.
- **Family revoke при reuse.** Когда `Consume` видит токен, у которого `consumed_at IS NOT NULL`, он делает `RevokeFamily(family_id)` перед возвратом ошибки. Это каноническая реакция на "stolen-token detected": invalidate каждого потомка скомпрометированного root-токена.
- **Никакой фоновой чистки expired.** Истёкшие строки остаются в таблице. Запускайте periodic `DELETE FROM auth_refresh_tokens WHERE expires_at < now() - interval '7 days'`, если хотите освобождать место.
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
