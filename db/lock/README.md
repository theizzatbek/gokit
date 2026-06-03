# db/lock

Postgres advisory-lock примитив — обёртка над `pg_try_advisory_lock`
(non-blocking) и `pg_advisory_lock` (blocking) на dedicated pool conn.
Имена хешатся в int64 ключ через первые 8 байт sha256, так что
одинаковый `name` всегда мапится в тот же lock-key через все реплики.

**Импорт:** `github.com/theizzatbek/gokit/db/lock`
**Зависит от:** `gokit/db` + pgx

## Зачем это нужно

`pg_advisory_lock` — нативный distributed-mutex Postgres'а, удобный
там, где сервис уже подключён к БД. Кит уже использует его внутри
`service.WithSingletonCron` для leader-election'а cron-job'ов;
этот пакет выставляет тот же примитив для app-кода — race-free
init одноразовых задач, distributed mutex для редко-выполняемых
критических секций, ad-hoc leader-election помимо cron'а.

## Quickstart

```go
import "github.com/theizzatbek/gokit/db/lock"

// One-liner: запустить fn ровно один раз через все реплики.
if err := lock.RunOnce(ctx, svc.DB, "orders.daily-rollup",
    func(ctx context.Context) error {
        return rollups.Run(ctx, svc.DB)
    }); err != nil {
    return err
}

// Низкоуровневый Try/release — для случаев, где успех/skip нужно
// разделить (логирование, метрики на выигравшую реплику).
lk := lock.New(svc.DB, "session.warmup")
acquired, release, err := lk.TryAcquire(ctx)
if err != nil { return err }
if !acquired { return nil /* другая реплика держит */ }
defer release()
// критическая секция
```

## API-поверхность

| Символ | Смысл |
|---|---|
| `lock.New(d, name, opts...) *Lock` | Конструирует. Panic'ит на nil d / пустом name (программерская ошибка). Опции — `WithLogger`, `WithMetrics`. |
| `(*Lock).TryAcquire(ctx) (acquired, release, err)` | Non-blocking. `acquired=false` — нормальный skip-путь. |
| `(*Lock).Acquire(ctx) (release, err)` | Blocking — ждёт лока или ctx-cancel. |
| `(*Lock).TryAcquireXact(ctx, tx) (acquired, err)` | Tx-scope (`pg_try_advisory_xact_lock`). Авто-release на commit/rollback — нет manual release. |
| `(*Lock).IsHeld(ctx) (held, err)` | Диагностика: TryAcquire → если contended, snapshot=held. Для `/admin`, не для control flow. |
| `(*Lock).Name() string` / `(*Lock).Key() int64` | Доступ к имени + derived int64-ключу для ops-запросов вроде `SELECT pid FROM pg_locks WHERE objid = $1`. |
| `lock.RunOnce(ctx, d, name, fn, opts...)` | Convenience: TryAcquire + run + release. Skip-fn'а на не-acquire. |
| `lock.RunBlocking(ctx, d, name, fn, opts...)` | Convenience: Acquire + run + release. Ждёт ctx-cancel. |

## Observability

```go
lk := lock.New(svc.DB, "orders.daily-rollup",
    lock.WithLogger(svc.Logger),
    lock.WithMetrics(svc.Metrics),
)
```

`WithLogger` пишет (`name=<lock-name>` в каждом attr):
- Debug на `lock: acquired`, `lock: contended`, `lock: released` (`held_ms=...`).
- Warn на `lock: acquire failed` (conn drop, server down).

`WithMetrics` регистрирует:
- `lock_acquires_total{name, outcome=acquired|contended|error}` — counter.
- `lock_hold_duration_seconds{name}` — histogram (buckets от 1ms до 10min).

Один collector на Lock-name. Второй `lock.New(d, "same-name", WithMetrics(reg))` против того же registry уйдёт в `duplicate metric` panic — wire каждое имя в одну `New` (та же конвенция, что у breaker/bulkhead).

## Transaction-scope: `TryAcquireXact`

Когда критическая секция уже обёрнута в `*db.Tx`, `pg_try_advisory_xact_lock` снимает необходимость в manual release — лок уходит вместе с tx commit/rollback:

```go
err := svc.DB.Tx(ctx, func(tx *db.Tx) error {
    lk := lock.New(svc.DB, "orders.batch-charge")
    ok, err := lk.TryAcquireXact(ctx, tx)
    if err != nil { return err }
    if !ok { return nil /* другая tx держит */ }

    // ... read + UPDATE rows ...
    return nil
})
```

Namespace — общий с session-level `TryAcquire`; смешивать оба варианта на одном имени безопасно. `tx == nil` → `*errs.Error{Code: lock_acquire_failed}`.

## Семантика

- **Session-level lock**: держится на dedicated pool conn до `release()` или до возврата conn в пул (panic-safe).
- **Авто-release на conn-close**: даже если `release` never called (paniccing handler), session-lock уходит сам, когда conn реап'ится пулом — не зависает forever.
- **Skip — не error**: `TryAcquire` возвращает `(false, nil, nil)` когда другой holder; ошибка только когда что-то реально сломалось (pool exhausted, DB down).
- **Deterministic key**: `Key()` — это `int64(big-endian(sha256(name)[:8]))`. Два одинаковых имени → один ключ; namespace per-service через префикс (`"orders.daily-rollup"`).

## Error codes

| Code | Смысл |
|---|---|
| `lock_acquire_failed` | pg_advisory_lock errored для non-cancel причин (conn drop, server down). |
| `lock_release_failed` | pg_advisory_unlock errored. Best-effort — session-level лок уходит сам. |
| `lock_nil_db` | `New(nil, …)` — programmer-error. |
| `lock_empty_name` | `New(d, "")` — programmer-error, пустые имена коллидировали бы. |

## См. также

- [`db`](../README.md) — обёртка над пулом
- [`service.WithSingletonCron`](../../service/README.md) — использует этот примитив внутри
</content>
