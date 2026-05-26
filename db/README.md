# db

pgx-based Postgres pool wrapper. `Connect(ctx, cfg, opts...) (*DB, error)` returns a `*DB` exposing `Query`/`QueryRow`/`Exec` (errors mapped to `*errs.Error`), `Tx(ctx, fn)` for functional transactions (savepoints on nested calls), `Healthcheck`, and a `Pool()` escape hatch.

**Import:** `github.com/theizzatbek/gokit/db`
**Depends on:** `jackc/pgx/v5`, `prometheus/client_golang`, `github.com/theizzatbek/gokit/errs`

## Why use it

`pgxpool` is great but every project then re-implements the same things: env-driven `Config`, slow-query logging, a transaction helper that propagates ctx, and pgx-error-to-domain-error mapping. `db.DB` is that bundle, with the gokit `*errs.Error` contract baked in so a service handler can `return db.Exec(...)` and the right HTTP status comes out.

## Quickstart

```go
import (
    "context"
    "github.com/caarlos0/env/v11"
    "github.com/theizzatbek/gokit/db"
)

type AppConfig struct {
    DB db.Config `envPrefix:"DB_"`
}

func main() {
    var cfg AppConfig
    _ = env.Parse(&cfg)

    d, err := db.Connect(context.Background(), cfg.DB,
        db.WithLogger(logger),
        db.WithSlowQueryThreshold(200*time.Millisecond),
        db.WithMetrics(promReg),
    )
    if err != nil { return err }
    defer d.Close()

    var id string
    err = d.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, "a@b.com").Scan(&id)
    // err is *errs.Error{Kind: NotFound} when no rows, etc.
}
```

## Configuration

`db.Config` (env-driven via `caarlos0/env/v11` — compose with your service Config under `envPrefix:"DB_"`):

| Field | Env | Default | Notes |
|---|---|---|---|
| `URL` | `DB_URL` | "" | full connection string; when set, identity fields below are ignored. See [Connection string via `URL`](#connection-string-via-url). |
| `Host` | `DB_HOST` | `localhost` | ignored if `URL` is set |
| `Port` | `DB_PORT` | `5432` | ignored if `URL` is set |
| `User` | `DB_USER` | — (required if no `URL`) | ignored if `URL` is set |
| `Password` | `DB_PASSWORD` | "" | empty for trust auth; ignored if `URL` is set |
| `Database` | `DB_NAME` | — (required if no `URL`) | ignored if `URL` is set |
| `SSLMode` | `DB_SSLMODE` | `disable` | `require`/`verify-full`/etc.; ignored if `URL` is set |
| `AppName` | `DB_APP_NAME` | "" | shown in `pg_stat_activity`; auto-set from `Service.NodeName` under `service.New`. See [Application name in `pg_stat_activity`](#application-name-in-pg_stat_activity). |
| `HasReadReplica` | `DB_HAS_READ_REPLICA` | `false` | opens a second pool against standbys. See [Read replicas](#read-replicas). |
| `MaxConns` | `DB_MAX_CONNS` | 10 | |
| `MinConns` | `DB_MIN_CONNS` | 0 | |
| `MaxConnLifetime` | `DB_MAX_LIFETIME` | 1h | |
| `MaxConnIdle` | `DB_MAX_IDLE` | 30m | |
| `ConnectTimeout` | `DB_CONN_TIMEOUT` | 5s | applied to initial connect |
| `ConnectMaxRetries` | `DB_CONNECT_MAX_RETRIES` | `0` (no retry) | K8s boot resilience |
| `ConnectBackoffBase` | `DB_CONNECT_BACKOFF_BASE` | `0` | K8s boot resilience |
| `ConnectBackoffMax` | `DB_CONNECT_BACKOFF_MAX` | `0` | K8s boot resilience |

### Connect retry (K8s boot resilience)

Three optional Config fields cap initial-Connect retry behaviour:

| Field | Env | Default |
|---|---|---|
| `ConnectMaxRetries` | `DB_CONNECT_MAX_RETRIES` | `0` (no retry) |
| `ConnectBackoffBase` | `DB_CONNECT_BACKOFF_BASE` | `0` (kit fail-fast) |
| `ConnectBackoffMax` | `DB_CONNECT_BACKOFF_MAX` | `0` |

Kit default is fail-fast (1 attempt). When using `gokit/service`,
the service auto-injects K8s-friendly defaults: 5 retries with 1s
base / 16s cap (~31s total). To disable, set
`DB_CONNECT_MAX_RETRIES=-1` or call `service.WithoutConnectRetry()`.

The retry loop respects `ctx.Done()` — a deadline-bounded ctx
aborts mid-backoff rather than hanging.

### Connection string via `URL`

`Config.URL` (env `DB_URL`) is the full postgres connection string. When set,
the individual fields (`Host`/`Port`/`User`/`Password`/`Database`/`SSLMode`)
are ignored.

```
DB_URL=postgres://app:s3cret@postgres-svc.default:5432/appdb?sslmode=disable
```

**Multi-host failover** is built into pgx — comma-separate the hosts inside the URL:

```
DB_URL=postgres://app:s3cret@h1,h2,h3:5432/appdb
```

pgx connects to whichever host satisfies `target_session_attrs=read-write`
(the kit always appends this to the primary pool's URL). On a master failover,
the pool reconnects to the new master automatically.

Note: `AppName` and `ConnectTimeout` are still merged into the URL as query
params when not already present — only the identity fields are fully ignored.

### Application name in `pg_stat_activity`

`Config.AppName` (env `DB_APP_NAME`) is sent to PostgreSQL during Connect and
appears in `pg_stat_activity.application_name`. When building a `*db.DB`
through `service.New`, the kit auto-sets this to `Service.NodeName` if you
left it empty — every pod is identifiable to DBAs as its K8s hostname.

To override per-environment, set `DB_APP_NAME=custom-name`.

### Options

| Option | Default | Notes |
|---|---|---|
| `WithLogger(*slog.Logger)` | silent | Logs errors + slow queries (when threshold set). nil = silent. |
| `WithSlowQueryThreshold(d)` | 0 (off) | Queries exceeding `d` are logged at WARN with full SQL + duration |
| `WithMetrics(prometheus.Registerer)` | no metrics | Registers `db_queries_total{op,status}`, `db_query_duration_seconds`, pool size gauges |

## Common patterns

### Single row → scan

```go
var u User
err := d.QueryRow(ctx,
    `SELECT id, email, created_at FROM users WHERE email = $1`, email,
).Scan(&u.ID, &u.Email, &u.CreatedAt)
// err is *errs.Error{Kind: NotFound, Code: "not_found"} on zero rows.
```

### Multiple rows

```go
rows, err := d.Query(ctx, `SELECT id, email FROM users WHERE org_id = $1`, orgID)
if err != nil { return nil, err }
defer rows.Close()

var users []User
for rows.Next() {
    var u User
    if err := rows.Scan(&u.ID, &u.Email); err != nil { return nil, err }
    users = append(users, u)
}
return users, rows.Err()
```

### Insert + RETURNING

```go
var inserted User
err := d.QueryRow(ctx, `
    INSERT INTO users(email, password_hash)
    VALUES($1, $2)
    RETURNING id, email, created_at`,
    email, hash,
).Scan(&inserted.ID, &inserted.Email, &inserted.CreatedAt)
// Unique-violation surfaces as *errs.Error{Kind: AlreadyExists, Code: "already_exists"}.
```

### Transactions

```go
err := d.Tx(ctx, func(tx *db.Tx) error {
    if _, err := tx.Exec(ctx, "INSERT INTO orders(...) VALUES($1)", id); err != nil {
        return err
    }
    if _, err := tx.Exec(ctx, "UPDATE inventory SET ..."); err != nil {
        return err
    }
    return nil  // commit
})
// Any returned error rolls back; *errs.Error preserved.
```

Nested `Tx` calls open a savepoint instead of a new transaction — composable from within already-transactional code.

### Healthcheck (for `/healthz`)

```go
if err := d.Healthcheck(ctx); err != nil {
    return errs.Unavailable("db_down", "postgres unhealthy")
}
```

### Escape hatch — raw pgx pool

```go
pool := d.Pool()  // *pgxpool.Pool
// Use pool.Acquire / pool.SendBatch / etc. directly. Errors are NOT mapped here.
```

## Read replicas

Set `Config.HasReadReplica = true` (env `DB_HAS_READ_REPLICA=true`) and the kit
opens a **second** internal pool against the same connection string with
`target_session_attrs=standby`. The single `*db.DB` you get back exposes two
sets of methods:

| Method | Pool | When to use |
|---|---|---|
| `Query` / `QueryRow` / `Exec` / `Tx` | primary (write) | mutations, reads-after-writes, `SELECT FOR UPDATE`, `INSERT/UPDATE/DELETE RETURNING` |
| `ReadQuery` / `ReadQueryRow` | read replica (falls back to primary when not configured) | replica-lag-tolerant reads: listings, search, analytics, plain GETs |

Example:

```go
// Write — always primary
row := db.QueryRow(ctx, `INSERT INTO links(...) VALUES (...) RETURNING id`, ...)

// Read that tolerates lag — read pool if configured, primary otherwise
rows, err := db.ReadQuery(ctx, `SELECT * FROM links WHERE user_id = $1`, userID)
```

**Requirements:** PostgreSQL **14+** (`target_session_attrs=standby` is PG 14+;
older PG only has `read-only`). The primary URL stays `target_session_attrs=read-write`.

**Behaviour on master failover:** pgx reconnects the primary pool to whichever
host in your multi-host URL now reports itself as read-write. No service
restart or env change needed. The read pool keeps targeting standbys.

**Behaviour when the standby pool can't connect at boot:** kit fails loud —
`db.Connect` returns `*errs.Error{Kind:KindUnavailable, Code:"db_unavailable"}`
and closes the primary pool before returning. Set `HasReadReplica=false` to opt out.

## Error model

Every method funnels its pgx error through `mapPgxErr` before returning:

| pgx situation | `*errs.Error` |
|---|---|
| `pgx.ErrNoRows` | `KindNotFound`, `Code: "not_found"` |
| `context.DeadlineExceeded` / `Canceled` | `KindTimeout`, `Code: "db_timeout"` |
| SQLSTATE `23505` (unique violation) | `KindAlreadyExists`, `Code: "already_exists"` |
| SQLSTATE `23503` (foreign-key violation) | `KindConflict`, `Code: "fk_violation"` |
| SQLSTATE `40001` (serialization failure) | `KindConflict`, `Code: "tx_conflict"` (retry-safe) |
| SQLSTATE `40P01` (deadlock) | `KindConflict`, `Code: "tx_conflict"` (retry-safe) |
| SQLSTATE `57014` (query cancelled by server) | `KindTimeout`, `Code: "db_timeout"` |
| SQLSTATE `08*` (connection errors) | `KindUnavailable`, `Code: "db_unavailable"` |
| Anything else | `KindInternal`, `Code: "db_failure"` |

The original `*pgconn.PgError` is preserved as `Cause`; use `errors.As` if you need its details (e.g. for ConstraintName-based branching).

## Observability

- **slog:** `WithLogger` enables ERROR on every wrapped failure (with SQL truncated to 1KB) and WARN on slow queries when `WithSlowQueryThreshold` is set.
- **Prometheus:** `WithMetrics(reg)` registers counters + histogram for queries and pool gauges. No metrics → zero collector overhead.

## Testing

Use [testcontainers-go/modules/postgres](https://golang.testcontainers.org/modules/postgres/) for integration tests against a real Postgres. Pattern from gokit's own tests (`db/testdb_test.go`):

```go
func startTestDB(t *testing.T) *db.DB {
    c, err := tcpg.Run(ctx, "postgres:16-alpine",
        tcpg.WithDatabase("test"), tcpg.WithUsername("test"), tcpg.WithPassword("test"),
        tcpg.BasicWaitStrategies(),
    )
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { _ = c.Terminate(ctx) })

    host, _ := c.Host(ctx)
    port, _ := c.MappedPort(ctx, "5432/tcp")
    p, _ := strconv.Atoi(port.Port())

    cfg := db.Config{Host: host, Port: p, User: "test", Password: "test", Database: "test", SSLMode: "disable"}
    d, _ := db.Connect(context.Background(), cfg)
    t.Cleanup(d.Close)
    return d
}
```

For per-test isolation, create a schema and `SET search_path` inside the test.

## Limitations

- **Postgres-only.** Hard dependency on pgx; no MySQL/SQLite adapter.
- **No ORM, no codegen.** Use `sqlc` separately if you want generated types.
- **No migration runner shipped.** Use `goose`, `tern`, or the naive `db.Exec(string(fileBytes))` pattern (see `examples/urlshort/main.go::applyMigrations`).
- **`Tx` rollback errors are logged, not returned.** A failed rollback after a failed commit is rare and not actionable; the original error wins.
- **`mapPgxErr` is opinionated.** SQLSTATE codes not in the switch fall through to `db_failure`. If you need a specific mapping, branch on `errors.As(err, &pgErr)`.

## See also

- [`db/sqb`](sqb/README.md) — opt-in squirrel wrapper with `$N` placeholders preconfigured
- [`errs`](../errs/README.md) — the error contract `db` returns
- [`auth/refreshpg`](../auth/refreshpg/README.md) — refresh-token store backed by `db.Querier`
- [`examples/urlshort`](../examples/urlshort/README.md) — full integration with migrations + handlers
