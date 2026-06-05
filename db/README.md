# db

pgx-based обёртка над Postgres pool. `Connect(ctx, cfg, opts...) (*DB, error)` возвращает `*DB`, выставляющий `Query`/`QueryRow`/`Exec` (ошибки маппятся в `*errs.Error`), `Tx(ctx, fn)` для функциональных транзакций (savepoint'ы при вложенных вызовах), `Healthcheck` и escape hatch `Pool()`.

**Импорт:** `github.com/theizzatbek/gokit/db`
**Зависит от:** `jackc/pgx/v5`, `prometheus/client_golang`, `github.com/theizzatbek/gokit/errs`

## Зачем это нужно

`pgxpool` отличный, но каждый проект потом переизобретает одно и то же: env-driven `Config`, slow-query логирование, transaction-хелпер, который пропагирует ctx, и pgx-error-to-domain-error маппинг. `db.DB` — это такой бандл, с контрактом `*errs.Error` от gokit, встроенным внутрь, так что service-handler может `return db.Exec(...)`, и правильный HTTP-статус выходит наружу.

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
    // err — *errs.Error{Kind: NotFound} когда нет строк, и т.д.
}
```

## Конфигурация

`db.Config` (env-driven через `caarlos0/env/v11` — компонуйте с вашим service-Config под `envPrefix:"DB_"`):

| Поле | Env | По умолчанию | Заметки |
|---|---|---|---|
| `URL` | `DB_URL` | "" | полная connection-строка; когда установлена, identity-поля ниже игнорируются. См. [Connection string через `URL`](#connection-string-через-url). |
| `Host` | `DB_HOST` | `localhost` | игнорируется если `URL` установлен |
| `Port` | `DB_PORT` | `5432` | игнорируется если `URL` установлен |
| `User` | `DB_USER` | — (обязательно если без `URL`) | игнорируется если `URL` установлен |
| `Password` | `DB_PASSWORD` | "" | пусто для trust-auth; игнорируется если `URL` установлен |
| `Database` | `DB_NAME` | — (обязательно если без `URL`) | игнорируется если `URL` установлен |
| `SSLMode` | `DB_SSLMODE` | `disable` | `require`/`verify-full`/и т.д.; игнорируется если `URL` установлен |
| `AppName` | `DB_APP_NAME` | "" | показывается в `pg_stat_activity`; авто-устанавливается из `Service.NodeName` под `service.New`. См. [Application name в `pg_stat_activity`](#application-name-в-pg_stat_activity). |
| `HasReadReplica` | `DB_HAS_READ_REPLICA` | `false` | открывает второй пул против standby. См. [Read replicas](#read-replicas). |
| `ReadURLs` | `DB_READ_URLS` (comma-separated) | пусто | список отдельных read-replica URL'ов. Перекрывает `HasReadReplica`. См. [Multi-replica routing](#multi-replica-routing). |
| `ReadStrategy` | `DB_READ_STRATEGY` | `round_robin` | `round_robin` или `random` — стратегия выбора replica при нескольких ReadURLs. |
| `LagBudget` | `DB_LAG_BUDGET` | `0` (off) | Cap на tracked replica lag; replicas над budget'ом скипаются router'ом. Эквивалентно `WithReadLagBudget(d)`. Требует `LagPollInterval`. |
| `LagPollInterval` | `DB_LAG_POLL_INTERVAL` | `0` (off) | Включает фоновую goroutine, которая polls лаг каждого replica. Эквивалентно `WithReplicaLagPolling(interval, threshold)`. |
| `LagPollThreshold` | `DB_LAG_POLL_THRESHOLD` | `0` (off) | WARN-threshold для polling goroutine. Имеет смысл только с `LagPollInterval > 0`. |
| `MaxConns` | `DB_MAX_CONNS` | 10 | |
| `MinConns` | `DB_MIN_CONNS` | 0 | |
| `MaxConnLifetime` | `DB_MAX_LIFETIME` | 1h | |
| `MaxConnIdle` | `DB_MAX_IDLE` | 30m | |
| `ConnectTimeout` | `DB_CONN_TIMEOUT` | 5s | применяется к initial-connect |
| `ConnectMaxRetries` | `DB_CONNECT_MAX_RETRIES` | `0` (no retry) | K8s boot-resilience |
| `ConnectBackoffBase` | `DB_CONNECT_BACKOFF_BASE` | `0` | K8s boot-resilience |
| `ConnectBackoffMax` | `DB_CONNECT_BACKOFF_MAX` | `0` | K8s boot-resilience |

### Connect retry (K8s boot-resilience)

Три опциональных Config-поля cap'ят initial-Connect retry-поведение:

| Поле | Env | По умолчанию |
|---|---|---|
| `ConnectMaxRetries` | `DB_CONNECT_MAX_RETRIES` | `0` (no retry) |
| `ConnectBackoffBase` | `DB_CONNECT_BACKOFF_BASE` | `0` (kit fail-fast) |
| `ConnectBackoffMax` | `DB_CONNECT_BACKOFF_MAX` | `0` |

Дефолт кита — fail-fast (1 попытка). При использовании `gokit/service`,
service авто-инжектит K8s-friendly defaults: 5 retries с 1s base / 16s cap (~31s total). Чтобы отключить, установите `DB_CONNECT_MAX_RETRIES=-1` или вызовите `service.WithoutConnectRetry()`.

Retry-loop уважает `ctx.Done()` — deadline-bounded ctx abort'ит mid-backoff, а не висит.

### Connection string через `URL`

`Config.URL` (env `DB_URL`) — полная postgres connection-строка. Когда
установлена, индивидуальные поля (`Host`/`Port`/`User`/`Password`/`Database`/`SSLMode`) игнорируются.

```
DB_URL=postgres://app:s3cret@postgres-svc.default:5432/appdb?sslmode=disable
```

**Multi-host failover** встроен в pgx — comma-separate хосты внутри URL:

```
DB_URL=postgres://app:s3cret@h1,h2,h3:5432/appdb
```

pgx соединяется с тем хостом, который удовлетворяет `target_session_attrs=read-write` (кит всегда append'ит это к URL'у primary-пула). На master-failover'е пул переподключается к новому master'у автоматически.

Note: `AppName` и `ConnectTimeout` всё равно мерджатся в URL как query-параметры, когда ещё не присутствуют — только identity-поля полностью игнорируются.

### Application name в `pg_stat_activity`

`Config.AppName` (env `DB_APP_NAME`) отправляется в PostgreSQL во время Connect и появляется в `pg_stat_activity.application_name`. При сборке `*db.DB` через `service.New`, кит авто-устанавливает это в `Service.NodeName`, если вы оставили его пустым — каждый pod идентифицируем DBA как его K8s-hostname.

Чтобы override'нуть per-environment, установите `DB_APP_NAME=custom-name`.

### Опции

| Опция | По умолчанию | Заметки |
|---|---|---|
| `WithLogger(*slog.Logger)` | silent | Логирует ошибки + slow queries (когда threshold установлен). nil = silent. |
| `WithSlowQueryThreshold(d)` | 0 (off) | Запросы, превышающие `d`, логируются на WARN с полным SQL + duration |
| `WithMetrics(prometheus.Registerer)` | нет метрик | Регистрирует `db_query_duration_seconds{outcome}` (histogram), `db_pool_size_total{pool,state}` (gauge), `db_tx_total{kind,outcome}` (counter), `db_tx_duration_seconds{kind,outcome}` (histogram), `db_slow_query_total` (counter; populated только когда `WithSlowQueryThreshold > 0`). `pool=primary\|standby` различает read-replica gauge'ы; `kind=tx\|savepoint` и `outcome=commit\|rollback\|panic` покрывают top-level vs nested. |
| `WithTracer(pgx.QueryTracer)` | none | Подключает external pgx-tracer рядом с внутренним logger/metrics tracer'ом кита. Кит композирует несколько tracer'ов через внутренний multi-tracer, так что логгер и внешний (например, `otelkit.NewPgxTracer()`) сосуществуют. `service.WithOtel` авто-подключает OTel pgx-tracer, когда DB также сконфигурирована; обращайтесь к `WithTracer` напрямую, только когда подключаете не-OTel tracing backend. |
| `WithReplicaLagPolling(interval, threshold)` | off | Spawn'ит фоновую goroutine, которая polls `pg_last_xact_replay_timestamp()` каждого read-replica каждые `interval`. Обновляет gauge `db_replica_lag_seconds{pool}` (когда `WithMetrics` тоже включён); при `threshold > 0` логирует WARN через `WithLogger`, когда lag превышает порог. Останавливается на `Close()`. |

## Common patterns

### Single row → scan

```go
var u User
err := d.QueryRow(ctx,
    `SELECT id, email, created_at FROM users WHERE email = $1`, email,
).Scan(&u.ID, &u.Email, &u.CreatedAt)
// err — *errs.Error{Kind: NotFound, Code: "not_found"} при zero rows.
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
// Unique-violation всплывает как *errs.Error{Kind: AlreadyExists, Code: "already_exists"}.
```

### Транзакции

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
// Любая возвращённая ошибка roll back'ит; *errs.Error сохраняется.
```

Вложенные вызовы `Tx` открывают savepoint вместо новой транзакции — компонуется из уже-транзакционного кода.

### Auto-retry на conflict (`TxRetry`)

Постгрес-ошибки `40001` (serialization failure) и `40P01` (deadlock detected) — это **retry-safe**: postgres гарантирует, что то же изменение можно безопасно применить повторно. `TxRetry` оборачивает `Tx` циклом auto-retry с exp-backoff + ±25% jitter:

```go
err := d.TxRetry(ctx, func(tx *db.Tx) error {
    // fn ДОЛЖНА быть идемпотентной — может запускаться повторно.
    if _, err := tx.Exec(ctx, `UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amt, from); err != nil {
        return err
    }
    _, err := tx.Exec(ctx, `UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amt, to)
    return err
},
    db.WithTxRetryMaxAttempts(5),                                      // default 3
    db.WithTxRetryBackoff(5*time.Millisecond, 100*time.Millisecond),   // base / cap
    db.WithTxRetryOpts(db.TxOpts{IsoLevel: db.Serializable}),          // SERIALIZABLE + retry — каноничный паттерн
)
```

Каждая retry-попытка инкрементирует counter `db_tx_retries_total` (первая не считается). Non-retryable ошибки всплывают на первой же попытке.

### Изоляция / read-only / deferrable (`TxWithOpts`)

```go
err := d.TxWithOpts(ctx, db.TxOpts{
    IsoLevel:       db.Serializable,
    AccessMode:     db.ReadOnly,
    DeferrableMode: db.Deferrable, // эффективно только при Serializable + ReadOnly
}, func(tx *db.Tx) error {
    // long-running analytic report
    return nil
})
```

`TxOpts{}` (нулевое значение) == текущий `Tx` (READ COMMITTED, read-write).

### Bulk insert через COPY (`CopyFrom`)

```go
n, err := d.CopyFrom(ctx,
    pgx.Identifier{"events"},
    []string{"id", "type", "payload"},
    pgx.CopyFromRows(batch),
)
```

Тонкая обёртка над `pgxpool.CopyFrom`, ошибки прогоняются через тот же `mapPgxErr`. `*Tx.CopyFrom` делает то же самое внутри транзакции (атомарно с окружающими `Exec`'ами).

### Защита от runaway-запросов (`WithDefaultStatementTimeout`, `WithConnInit`)

`statement_timeout` ставится на **сервере**, поэтому убивает зависший запрос даже когда caller-context уже отвалился:

```go
d, err := db.Connect(ctx, cfg,
    db.WithDefaultStatementTimeout(30*time.Second),
    db.WithConnInit(func(ctx context.Context, conn *pgx.Conn) error {
        _, err := conn.Exec(ctx, `SET search_path TO app, public`)
        return err
    }),
)
```

`WithConnInit` — общий hook, вызывается один раз на свежее pgx-соединение **до** того, как оно попадает в пул. Несколько `WithConnInit` накапливаются по порядку регистрации. Используйте для `SET application_name`, `SET search_path`, prewarming prepared-statement кэша или `SET ROLE` для tenant-изоляции.

### Healthcheck (для `/healthz`)

```go
if err := d.Healthcheck(ctx); err != nil {            // primary pool only
    return errs.Unavailable("db_down", "postgres unhealthy")
}
if err := d.HealthcheckRead(ctx); err != nil {        // standby pool (no-op when not configured)
    // logger.Warn("read replica unhealthy", "err", err) — НЕ фейлим /readyz
}
```

Split-API на цели: `/readyz` зависит от primary (без него writes невозможны), `HealthcheckRead` — диагностический сигнал. `ReadQuery` прозрачно fallback'ится на primary, когда replica не сконфигурирован, но НЕ ловит "half-dead" standby с зависающими соединениями — отдельный пинг это покрывает.

### Escape hatch — raw pgx pool

```go
pool := d.Pool()  // *pgxpool.Pool
// Используйте pool.Acquire / pool.SendBatch / и т.д. напрямую. Ошибки тут НЕ маппятся.
```

## Read replicas

Установите `Config.HasReadReplica = true` (env `DB_HAS_READ_REPLICA=true`), и
кит откроет **второй** внутренний пул против той же connection-строки с
`target_session_attrs=standby`. Один `*db.DB`, который вы получите назад, выставляет два набора методов:

| Метод | Пул | Когда использовать |
|---|---|---|
| `Query` / `QueryRow` / `Exec` / `Tx` | primary (write) | mutations, reads-after-writes, `SELECT FOR UPDATE`, `INSERT/UPDATE/DELETE RETURNING` |
| `ReadQuery` / `ReadQueryRow` | read replica (fallback на primary, когда не сконфигурирован) | replica-lag-tolerant reads: listings, search, analytics, plain GET'ы |

Пример:

```go
// Write — всегда primary
row := db.QueryRow(ctx, `INSERT INTO links(...) VALUES (...) RETURNING id`, ...)

// Read, который tolerates lag — read pool если сконфигурирован, иначе primary
rows, err := db.ReadQuery(ctx, `SELECT * FROM links WHERE user_id = $1`, userID)
```

**Требования:** PostgreSQL **14+** (`target_session_attrs=standby` появился в PG 14; более старые PG имеют только `read-only`). Primary-URL остаётся `target_session_attrs=read-write`.

**Boot-time retry budget:** когда `HasReadReplica=true`, кит запускает connect-retry loop против каждого пула последовательно. Total wait на boot'е может быть примерно **2× single-pool budget'а** (с дефолтным `ConnectMaxRetries=5` / `ConnectBackoffMax=16s`, ≈30s → ≈60s в худшем случае). Размерьте `failureThreshold` × `periodSeconds` в K8s readiness probe соответственно, или понизьте `DB_CONNECT_MAX_RETRIES`, если предпочитаете restart-and-retry, а не wait на boot'е.

**Поведение при master-failover'е:** pgx переподключает primary-пул к тому хосту в вашем multi-host URL, который теперь репортит себя как read-write. Restart сервиса или env-изменение не нужны. Read-пул продолжает таргетить standby'и.

**Поведение, когда standby-пул не может подключиться на boot'е:** кит фейлится loud — `db.Connect` возвращает `*errs.Error{Kind:KindUnavailable, Code:"db_unavailable"}` и закрывает primary-пул перед возвратом. Установите `HasReadReplica=false`, чтобы отказаться.

## Multi-replica routing

Для развёртываний с несколькими отдельными standby-endpoint'ами (геораспределённые replica, dedicated reporting replica, per-replica role separation) установите `Config.ReadURLs` — массив отдельных connection-строк, каждая со своими credentials/host/sslmode:

```bash
DB_READ_URLS=postgres://app:p@rep1.az-a:5432/appdb,postgres://app:p@rep2.az-b:5432/appdb,postgres://reports:p@rep-reports:5432/appdb?target_session_attrs=any
```

Кит откроет по pgxpool на каждый URL. URL без `target_session_attrs` авто-получит `standby` (PG 14+); передайте параметр явно (`target_session_attrs=any`), чтобы override'нуть для analytics-replica, которая может быть promoted в primary.

`ReadURLs` перекрывает `HasReadReplica` — если оба заданы, `HasReadReplica` игнорируется.

### Полная env-driven multi-replica setup

`db.Connect` авто-применяет `WithReplicaLagPolling` + `WithReadLagBudget` из Config-полей `LagPollInterval` / `LagPollThreshold` / `LagBudget` (env: `DB_LAG_POLL_INTERVAL` / `DB_LAG_POLL_THRESHOLD` / `DB_LAG_BUDGET`). Полная production-ready setup из env:

```bash
DB_URL=postgres://app:p@primary:5432/appdb
DB_READ_URLS=postgres://app:p@rep1:5432/appdb,postgres://app:p@rep2:5432/appdb
DB_READ_STRATEGY=round_robin
DB_LAG_POLL_INTERVAL=10s
DB_LAG_POLL_THRESHOLD=30s
DB_LAG_BUDGET=5s
```

Сервис под `service.New` подхватит всё автоматически — service.Config embed'ит db.Config с `envPrefix:"DB_"`. **Config-derived опции применяются BEFORE user-supplied** (через `service.WithDBOptions` / прямой вызов `db.Connect`'у с opts), так что explicit programmatic configuration перекрывает env-defaults через last-write-wins.

### Стратегия выбора

```bash
DB_READ_STRATEGY=round_robin   # default — atomic counter, без блокировок
DB_READ_STRATEGY=random        # uniform, math/rand/v2
```

`ReadQuery` / `ReadQueryRow` диспатчит запросы по этой стратегии. Кит **не** делает health-aware skipping mid-flight — для observability используйте `WithReplicaLagPolling` + Prometheus alert; для удаления больного replica перевыкатите с обновлённым `DB_READ_URLS`.

### Read-your-writes (force primary)

После write-транзакции subsequent read может race'ить с replica-лагом. Заверните ctx в `db.ReadFromPrimary` чтобы насильно отрутить запрос на primary:

```go
err := svc.DB.Tx(ctx, func(tx *db.Tx) error {
    _, err := tx.Exec(ctx, `INSERT INTO orders ... RETURNING id`, ...)
    return err
})
if err != nil { return err }

// На no-replica deployment'е ReadFromPrimary — deterministic no-op
// (ReadQuery всё равно fall back'ится на primary).
row := svc.DB.ReadQueryRow(db.ReadFromPrimary(ctx),
    `SELECT total FROM orders_summary WHERE order_id = $1`, id)
```

### API-поверхность

| Метод | Заметки |
|---|---|
| `(d) ReadPool() *pgxpool.Pool` | Первый read-pool для back-compat. `nil` когда replica нет. |
| `(d) ReadPools() []ReadPoolInfo` | Все read-pool'ы с их именами (`standby` или `standby-N`). |
| `(d) HasReadReplica() bool` | True если хотя бы один replica настроен. |

## Replication observability

`(d) ReplicationLag(ctx) []ReplicaLagInfo` — snapshot текущего replication-лага каждого replica:

```go
infos := svc.DB.ReplicationLag(ctx)
for _, info := range infos {
    if !info.Healthy {
        logger.Warn("replica down", "pool", info.PoolName, "err", info.Err)
        continue
    }
    fmt.Printf("%s: %.2fs behind\n", info.PoolName, info.LagSeconds)
}
```

- Запрашивает `EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp()))` на каждом read-pool'е.
- Primary node (когда оператор по ошибке указал DB_READ_URLS на writable instance) возвращает NULL → кит репортит `LagSeconds=0, Healthy=true`.
- Empty slice (не error) когда replica не настроен.

### Continuous monitoring

```go
d, _ := db.Connect(ctx, cfg,
    db.WithLogger(logger),
    db.WithMetrics(promReg),
    db.WithReplicaLagPolling(10*time.Second, 30*time.Second),
)
```

- Фоновая goroutine polls каждый replica каждые `interval` (первый sample immediately).
- Метрика `db_replica_lag_seconds{pool}` обновляется per-tick (`-1` когда probe failed).
- При `threshold > 0` — WARN-log через `WithLogger`, когда lag превышает порог.
- Goroutine завершается на `Close()` (включая `Drain` через `service.OnShutdown`).
- No-op когда не настроен replica либо `interval ≤ 0`.

### Smart read-routing (`WithReadLagBudget`)

Поверх lag-polling'а `WithReadLagBudget(d)` превращает router в health-aware: replicas с `tracked lag > d` или с failed lag-probe пропускаются, и `ReadQuery` fallback'ится на primary когда все replicas отфильтрованы:

```go
d, _ := db.Connect(ctx, cfg,
    db.WithMetrics(promReg),
    db.WithReplicaLagPolling(10*time.Second, 30*time.Second),
    db.WithReadLagBudget(5*time.Second),  // > 5s lag → skip
)
```

| Метрика | Заметки |
|---|---|
| `db_replica_skipped_total{pool, reason="unhealthy"}` | Probe failed, replica помечен `healthy=false`. Revive на следующем успешном probe'е. |
| `db_replica_skipped_total{pool, reason="over_budget"}` | `tracked_lag > budget`. |
| `db_replica_fallback_total` | Все replica отфильтрованы → запрос ушёл на primary. **Это alert-signal деградации.** |

Freshly-started replica (нет ещё ни одного probe'а) считается healthy + in-budget — kit favours optimism чтобы не surprise'ить caller'а во время первого polling-интервала.

`ReadPoolInfo` теперь содержит `Healthy bool` + `LagSeconds float64` — admin-эндпоинты могут отрендерить per-replica state без дополнительного запроса.

## Query-helpers (ergonomics)

Тонкие обёртки над повторяющимися паттернами. Все принимают `db.Querier`, так что работают с `*DB` и `*Tx` одинаково.

```go
// SELECT EXISTS(...) → bool
ok, err := db.Exists(ctx, svc.DB,
    `SELECT 1 FROM users WHERE email = $1`, email)

// SELECT count(*) FROM (...) → int64
n, err := db.Count(ctx, svc.DB,
    `SELECT 1 FROM events WHERE created_at >= $1`, since)

// Single-column → []T (generic)
ids, err := db.Pluck[string](ctx, svc.DB,
    `SELECT id FROM users WHERE org_id = $1`, orgID)

// Single-row single-column → T
email, err := db.Get[string](ctx, svc.DB,
    `SELECT email FROM users WHERE id = $1`, userID)

// "no rows" classifier — заменяет errors.As + .Kind == NotFound
if err != nil && db.NotFound(err) { return nil }
```

Все маппят ошибки через тот же `mapPgxErr`. `Pluck` возвращает empty slice (not nil) когда rows.Next пуст. Все nil-safe (вызов с nil Querier → `*errs.Error{KindValidation, Code: "db_nil_querier"}`).

## Batch (one round-trip multi-statement)

`db.NewBatch().Queue(...).Queue(...)` собирает N statements, `(d) SendBatch(ctx, b)` (или `(tx) SendBatch`) шипит их за один round-trip через pgx extended-query protocol:

```go
b := db.NewBatch().
    Queue(`UPDATE accounts SET balance = balance - $1 WHERE id = $2`, amt, from).
    Queue(`UPDATE accounts SET balance = balance + $1 WHERE id = $2`, amt, to).
    Queue(`INSERT INTO ledger(from_acc, to_acc, amount) VALUES ($1, $2, $3)`, from, to, amt)

res, err := svc.DB.Tx(ctx, func(tx *db.Tx) error {
    br, err := tx.SendBatch(ctx, b)
    if err != nil { return err }
    defer br.Close()
    if _, err := br.Exec(); err != nil { return err }
    if _, err := br.Exec(); err != nil { return err }
    if _, err := br.Exec(); err != nil { return err }
    return nil
})
```

Результаты iterate'аются `Exec()` / `Query()` / `QueryRow()` в **порядке Queue'а** — pgx pipeline'ит протокол в том же порядке. Над-iteration → `*errs.Error{Code: "db_batch_overrun"}`. Не забывайте `defer br.Close()` — leaked BatchResults удерживает pgx-conn до конца процесса.

Для bulk-insert'а (тысячи row'ов) используйте `CopyFrom` — у Batch'а есть per-statement protocol-overhead.

## Query-name tagging

`db.WithQueryName(ctx, "user_lookup")` тегает все queries под этим ctx именем — `db_query_duration_seconds` получает label `name="user_lookup"`:

```go
ctx = db.WithQueryName(ctx, "user_lookup")
err := svc.DB.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&id)
// → db_query_duration_seconds{name="user_lookup", outcome="success"}
```

**Cardinality safety:** label значение consumed verbatim — НИКОГДА не используйте user-controlled input. Ограничьте имена small fixed set per service (`"user_lookup"`, `"list_orders"`, `"outbox_drain"`) — runaway name set взорвёт metrics registry. Reach for `WithQueryName` только когда per-query slice-and-dice analytics реально нужны; для common case unlabelled aggregate (`name=""`) достаточен.

Nested `WithQueryName` — last write wins; outer name перетирается для queries под inner ctx.

## Connection pinning (`WithConn`)

`(d) WithConn(ctx, fn func(*pgxpool.Conn) error) error` — acquire'ит один conn из primary-пула, передаёт его в `fn`, releases на return. Используйте когда последовательность query'ев ДОЛЖНА landить на один и тот же physical connection: temp tables (session-scoped), prepared statements reused across multiple Exec/Query, cursor FETCH, `SET LOCAL` semantics вне транзакции.

```go
err := svc.DB.WithConn(ctx, func(conn *pgxpool.Conn) error {
    if _, err := conn.Exec(ctx, `CREATE TEMP TABLE stage (id int)`); err != nil {
        return err
    }
    if _, err := conn.Exec(ctx, `INSERT INTO stage SELECT generate_series(1, 1000)`); err != nil {
        return err
    }
    _, err := conn.Exec(ctx, `INSERT INTO users SELECT id FROM stage`)
    return err
    // stage автоматически дропается когда conn возвращается в пул.
})
```

`(d) WithReadConn(ctx, fn)` — read-replica variant с тем же routing'ом, что и ReadQuery (round-robin / random / lag-budget). Fallback на primary когда нет replica или все отфильтрованы. `db.ReadFromPrimary(ctx)` форсит primary conn.

Ошибки `fn` всплывают verbatim (no mapPgxErr wrap); Acquire/Release failure маппится в `*errs.Error{KindUnavailable, Code: "db_unavailable"}`.

## Pool snapshot (`Stats`)

`(d) Stats() Stats` возвращает snapshot всех pgxpool stats + replica health/lag за один cheap call. Подходит для `/admin` эндпоинтов, которые рендерят small dashboard без scraping'а Prometheus:

```go
s := svc.DB.Stats()
fmt.Printf("primary: %d/%d acquired\n", s.Primary.Acquired, s.Primary.Max)
if s.HasReplicas {
    for _, r := range s.Replicas {
        status := "healthy"
        if !r.Healthy { status = "DOWN" }
        fmt.Printf("%s [%s]: lag=%.2fs, %d/%d acquired\n",
            r.Name, status, r.LagSeconds, r.Acquired, r.Max)
    }
}
```

`PoolStats{Name, Acquired, Idle, Max, Total, Healthy, LagSeconds}` — `Name` matches the `pool=` label кит'овских Prometheus collectors. `Healthy + LagSeconds` populated только для replica'ов (для primary всегда zero-value).

## Keyset pagination (`Paginate`)

Cursor-based pagination через канонический `WHERE key > $cursor ORDER BY key LIMIT N+1` pattern. Кит управляет cursor encoding'ом + сборкой slice'а, caller отвечает за SQL.

```go
type userKey struct{ ID string }
type User struct { ID, Email string }

scan := func(rows pgx.Rows) (User, userKey, error) {
    var u User
    err := rows.Scan(&u.ID, &u.Email)
    return u, userKey{ID: u.ID}, err
}

prev, _ := db.DecodeCursor[userKey](req.Cursor)  // "" → zero
page, err := db.Paginate[User, userKey](ctx, svc.DB, `
    SELECT id, email FROM users
    WHERE ($1::text = '' OR id > $1)
    ORDER BY id
    LIMIT $2
`, 20, scan, prev.ID, 21)  // limit+1 — кит uses +1 для detection

// page.Items: до 20 user'ов
// page.NextCursor: opaque string — pass обратно в req.Cursor для next page
```

Cursor — base64(JSON) под капотом; кит guarantees round-trip safety через `EncodeCursor[K]` / `DecodeCursor[K]`. On-wire format unspecified — НЕ парсите сами.

Malformed cursor → `*errs.Error{KindValidation, Code: "db_cursor_invalid"}` (surfaces как HTTP 400 через `errs.HTTP`).

## Bulk UPDATE (`BulkUpdate`)

Builder для `UPDATE table SET … FROM (VALUES …) AS t(…) WHERE table.key = t.key` — single round-trip для "у меня N rows и N новых values для каждого":

```go
n, err := db.NewBulkUpdate("users", "id").
    Columns("email", "display_name").
    Add("u-1", "alice@new.example", "Alice").
    Add("u-2", "bob@new.example", "Bob").
    Exec(ctx, svc.DB)
// n = 2 (rows affected)
```

Stable Codes для validation failures (`db_bulk_no_table`, `db_bulk_no_key`, `db_bulk_no_columns`, `db_bulk_row_arity`). Empty bulk (no `Add` calls) — no-op, returns (0, nil), DB не touch'ится.

Для thousands of rows предпочтительнее `CopyFrom` в temp table + single UPDATE — VALUES list grows linearly с row count.

## Error-модель

Каждый метод прогоняет свою pgx-ошибку через `mapPgxErr` перед возвратом:

| pgx-ситуация | `*errs.Error` |
|---|---|
| `pgx.ErrNoRows` | `KindNotFound`, `Code: "not_found"` |
| `context.DeadlineExceeded` / `Canceled` | `KindTimeout`, `Code: "db_timeout"` |
| SQLSTATE `23505` (unique violation) | `KindAlreadyExists`, `Code: "already_exists"` |
| SQLSTATE `23503` (foreign-key violation) | `KindConflict`, `Code: "fk_violation"` |
| SQLSTATE `40001` (serialization failure) | `KindConflict`, `Code: "tx_conflict"` (retry-safe) |
| SQLSTATE `40P01` (deadlock) | `KindConflict`, `Code: "tx_conflict"` (retry-safe) |
| SQLSTATE `57014` (query cancelled by server) | `KindTimeout`, `Code: "db_timeout"` |
| SQLSTATE `08*` (connection errors) | `KindUnavailable`, `Code: "db_unavailable"` |
| Всё остальное | `KindInternal`, `Code: "db_failure"` |

Оригинальный `*pgconn.PgError` сохраняется как `Cause`; используйте `errors.As`, если нужны детали (например, для ConstraintName-based branching).

## Observability

- **slog:** `WithLogger` включает ERROR на каждую обёрнутую failure (с SQL, обрезанным до 1KB) и WARN на slow queries, когда `WithSlowQueryThreshold` установлен.
- **Prometheus:** `WithMetrics(reg)` регистрирует counters + histogram для запросов и pool-gauges. Нет метрик → zero collector overhead.

## Тестирование

Используйте [testcontainers-go/modules/postgres](https://golang.testcontainers.org/modules/postgres/) для интеграционных тестов против реального Postgres. Паттерн из собственных тестов gokit (`db/testdb_test.go`):

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

Для per-test изоляции создавайте schema и `SET search_path` внутри теста.

## Ограничения

- **Только Postgres.** Hard-зависимость от pgx; нет MySQL/SQLite адаптера.
- **Никакого ORM, никакого codegen.** Используйте `sqlc` отдельно, если хотите generated types.
- **Migration runner не поставляется.** Используйте `goose`, `tern` или наивный паттерн `db.Exec(string(fileBytes))` (см. `examples/urlshort/main.go::applyMigrations`).
- **Ошибки `Tx`-rollback логируются, а не возвращаются.** Failed rollback после failed commit редок и не actionable; оригинальная ошибка побеждает.
- **`mapPgxErr` opinionated.** SQLSTATE-коды не из switch проваливаются в `db_failure`. Если нужен специфический mapping, ветвитесь по `errors.As(err, &pgErr)`.

## См. также

- [`db/sqb`](sqb/README.md) — опциональная squirrel-обёртка с `$N` placeholders предконфигурированными
- [`db/outbox`](outbox/README.md) — паттерн transactional-outbox поверх Postgres для at-least-once event publish
- [`errs`](../errs/README.md) — error-контракт, который возвращает `db`
- [`auth/refreshpg`](../auth/refreshpg/README.md) — refresh-token store, backed by `db.Querier`
- [`examples/urlshort`](../examples/urlshort/README.md) — полная интеграция с миграциями + хендлерами
</content>
