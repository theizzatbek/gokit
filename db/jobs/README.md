# db/jobs

Postgres-backed delayed-job queue. Закрывает gap между cron'ом
(периодика) и outbox'ом (cross-service publishing): one-shot
scheduled work — "send welcome 5 мин после signup'а", "retry
webhook через час", "cleanup upload через 7 дней".

**Импорт:** `github.com/theizzatbek/gokit/db/jobs`
**Зависит от:** `db` + `prometheus/client_golang` + `errs`

## Quickstart

```go
import "github.com/theizzatbek/gokit/db/jobs"

w, _ := jobs.NewWorker(svc.DB,
    jobs.WithInterval(time.Second),
    jobs.WithWorkerID(svc.NodeName),
    jobs.WithLogger(svc.Logger()),
    jobs.WithMetrics(svc.MetricsRegistry()))

type Welcome struct { UserID string `json:"user_id"` }

jobs.RegisterHandler[Welcome](w, "user.welcome", func(ctx context.Context, p Welcome) error {
    return mailer.SendWelcome(ctx, p.UserID)
})

go w.Start(ctx)  // blocks; pair with svc.OnShutdown(w.Stop)

// Enqueue from anywhere — including inside a Tx:
_, _ = jobs.Schedule(ctx, svc.DB,
    time.Now().Add(5*time.Minute),
    "user.welcome", Welcome{UserID: u.ID})
```

## Schema

`jobs.Schema()` возвращает идемпотентный DDL. Apply через `jobs.ApplySchema(ctx, db)` или включите в migration-tool.

```sql
CREATE TABLE jobs (
    id           bigserial PRIMARY KEY,
    type         text NOT NULL,
    queue        text NOT NULL DEFAULT 'default',
    payload      jsonb NOT NULL,
    run_at       timestamptz NOT NULL DEFAULT NOW(),
    state        text NOT NULL DEFAULT 'queued',
    attempts     int NOT NULL DEFAULT 0,
    max_attempts int NOT NULL DEFAULT 25,
    last_error   text,
    locked_by    text,
    locked_at    timestamptz,
    created_at   timestamptz NOT NULL DEFAULT NOW(),
    finished_at  timestamptz
);
```

State-machine: `queued → running → done | queued (retry) | failed`.

## Concurrency-model

- Worker.tick claim'ит до `BatchSize` rows через `UPDATE ... WHERE id IN (SELECT ... FOR UPDATE SKIP LOCKED)` — multi-pod-safe из коробки. Один worker никогда не trample'ит другого.
- Один Worker per pod, N pods = N workers, все полят одну таблицу. Throughput scales linearly с pod-count до DB-bottleneck'а.
- Sticky-failures: после `max_attempts` row остаётся в `state='failed'` для operator triage — никогда silent-drop'а.

## Backoff

Exponential с ±10% jitter, capped at 1h:
- attempt 1 → ~1s
- attempt 5 → ~16s
- attempt 10 → ~17min
- attempt 25 → 1h (cap)

`max_attempts=25` ≈ 24h budget — после этого row moves to `failed`.

## API-поверхность

| Функция | Заметки |
|---|---|
| `Schedule[T](ctx, q, runAt, type, payload, opts...)` | INSERT one row. q — любой `db.Querier` (включая Tx → transactional enqueue). zero-runAt = "run ASAP". |
| `NewWorker(d, opts...)` | Конструктор. Single-use (Start callable once). |
| `RegisterHandler[T](w, type, fn)` | Typed handler. Panic'ит на duplicate. |
| `Start(ctx)` | Blocks. Returns ctx.Err() on cancel. |
| `Stop()` | Signals shutdown + waits for current tick to finish. Idempotent. |
| `Schema()` / `ApplySchema(ctx, d)` | DDL helpers. |

## Опции

| Worker option | Default | Заметки |
|---|---|---|
| `WithInterval(d)` | 1s | Polling cadence. |
| `WithBatchSize(n)` | 50 | Max rows claimed per tick. |
| `WithWorkerID(id)` | `jobs-worker` | Stamps `locked_by` для operator triage. Стабильное имя pod'а лучше дефолта. |
| `WithQueues(names...)` | all queues | Drain subset — "email-pool" / "billing-pool". |
| `WithLogger(l)` | nil | Debug на dispatch, Warn на errors. |
| `WithMetrics(reg)` | nil | `jobs_processed_total{type,outcome}`, `jobs_dispatch_duration_seconds`, `jobs_inflight`, `jobs_poll_errors_total`. |

| Schedule option | Default | Заметки |
|---|---|---|
| `WithQueue(name)` | `default` | Бакет для очередей. |
| `WithMaxAttempts(n)` | 25 | Cap retries. |

## Когда что выбирать

| Use-case | Tool |
|---|---|
| Periodic ("каждые 5 мин"; cron-style) | `service.WithCronJob` |
| Cross-service event ("user.signed_up" → 3 subscribers) | `db/outbox` + `clients/natsmap` |
| One-shot delayed ("welcome email через 30 мин") | `db/jobs` (this) |
| In-process bulk ("batch 100 metrics into one InfluxDB write") | `batch` |

## Ограничения

- **Worker не consume'ит unknown types** — пока ни один handler не зарегистрирован, row моментально moves to `failed` (Warn-log). Это feature, не bug — silently-skip'ить unknown types скрыл бы wiring-bugs.
- **JSON-payload limit ~256MB** (Postgres JSONB hard cap). Реалистично — никогда не close.
- **Single-table per service** (today). Multi-tenant routing — добавляйте `tenant_id` колонку через `ALTER` и filter в свой `WithQueues`-обёртке.

## См. также

- [`db/outbox`](../outbox/README.md) — transactional event-publishing
- [`db/lock`](../lock/README.md) — advisory-lock primitive (используется в Worker'е для distributed-coordination, если потребуется)
- [`service.WithCronJob`](../../service/README.md) — periodic task primitive
</content>
