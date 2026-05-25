# auth/refreshpg

Postgres-backed `auth.RefreshStore` over `db.Querier`. Atomic `Consume` via single `UPDATE … RETURNING`; reuse detection triggers a family-wide `RevokeFamily` before returning `*errs.Error{Code: "refresh_reused"}`. DDL lives in `schema.sql` — the package itself does not run migrations.

**Parent:** [../README.md](../README.md)
**Import:** `github.com/theizzatbek/gokit/auth/refreshpg`

## Use

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

`refreshpg.New(d)` accepts any `db.Querier` — `*db.DB` or `*db.Tx`. Tests can pass a transaction so changes roll back at test end.

## Schema

Apply `schema.sql` (or copy the DDL into your migration) before first use:

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

`examples/urlshort/migrations/0001_init.sql` includes this DDL verbatim alongside the service's own tables.

## Notes

- **Token hashes, not tokens.** The raw refresh token never lands in the DB — only `sha256(token)` does. A DB leak doesn't compromise active refresh tokens.
- **Family revoke on reuse.** When `Consume` sees a token whose `consumed_at IS NOT NULL`, it `RevokeFamily(family_id)` before returning the error. This is the canonical "stolen-token detected" response: invalidate every descendant of the compromised root token.
- **No background expiry cleanup.** Expired rows stay in the table. Run a periodic `DELETE FROM auth_refresh_tokens WHERE expires_at < now() - interval '7 days'` if you want to reclaim space.
- **Atomic via `UPDATE … RETURNING`.** No SELECT-then-UPDATE race window. Diagnostic `SELECT` on miss path classifies whether the token never existed vs. was already consumed.
- **`SecurityLogger`** on `*auth.Auth` (via `auth.WithSecurityLogger`) emits structured WARN events for reuse-triggered revocations — wire to your SIEM/alerting.

## Testing

Use [testcontainers-go/modules/postgres](https://golang.testcontainers.org/modules/postgres/). Pattern from gokit's own `store_test.go`:

```go
ctx := context.Background()
c, _ := tcpostgres.Run(ctx, "postgres:16-alpine",
    tcpostgres.WithDatabase("test"), tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
    tcpostgres.BasicWaitStrategies())
defer testcontainers.TerminateContainer(c)

d, _ := db.Connect(ctx, /* derive cfg from c */)
_, _ = d.Exec(ctx, refreshpg.SchemaSQL())  // or inline the DDL
store := refreshpg.New(d)
```

## See also

- [`auth`](../README.md) — parent: `WithRefreshStore` consumes this
- [`auth/refreshredis`](../refreshredis/README.md) — same contract, Redis-backed
- [`db`](../../db/README.md) — provides the `Querier` interface
