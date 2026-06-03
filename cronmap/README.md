# cronmap

Declarative cron scheduler для kit-based сервисов. Jobs живут в
`crons.yaml`; Go-код регистрирует handlers по имени. Симметричен
[`fibermap`](../fibermap/README.md) (HTTP routes), [`clients/apimap`](../clients/apimap/README.md) (outbound calls),
[`clients/natsmap`](../clients/natsmap/README.md) (pub/sub).

**Импорт:** `github.com/theizzatbek/gokit/cronmap`
**Зависит от:** `errs/` + `robfig/cron/v3` + `gopkg.in/yaml.v3` (+ опционально `db/lock` для PGLocker'а, `sentrykit` для slug-monitoring'а)

## Когда использовать

- Есть набор periodic-jobs которые вы хотите видеть в одном файле, а не разбросанными по `WithCron(...)` строкам в main.go.
- Хотите менять schedule без redeploy (через mounted config).
- Нужны cross-cutting features (per-run timeout, singleton leader-elect, Sentry crons slug) как YAML-аттрибуты, не как набор отдельных `With*` опций.

Не нужен, если:
- Job'ы computed at startup (например, randomised stagger) — используйте [`service.WithCron`](../service/README.md).
- Нужен payload-carrying job queue ("send welcome 5 мин после signup") — используйте [`db/jobs`](../db/jobs/README.md).
- Нужна in-process serialisation одного job'а (no overlap) — это v2 (пока используйте `singleton: true` через [PGLocker](#singleton)).

## Quickstart

`crons.yaml`:

```yaml
jobs:
  - name: daily-rollup
    handler: rollups.daily
    schedule: "0 3 * * *"      # 03:00 daily
    timeout: 5m                 # per-run deadline
    singleton: true             # leader election via Locker
    sentry_slug: orders-daily-rollup
  - name: hourly-cleanup
    handler: cleanup.hourly
    schedule: "@hourly"
```

`main.go`:

```go
import "github.com/theizzatbek/gokit/cronmap"

eng := cronmap.New()
if err := eng.LoadFile("crons.yaml"); err != nil { return err }

cronmap.RegisterHandler(eng, "rollups.daily",
    func(ctx context.Context) error { return rollups.Daily(ctx, db) })
cronmap.RegisterHandler(eng, "cleanup.hourly",
    func(ctx context.Context) error { return cleanup.Hourly(ctx, db) })

rt, err := eng.Build(
    cronmap.WithLogger(logger),
    cronmap.WithMetrics(reg),
    cronmap.WithSingletonLocker(cronmap.PGLocker(db)), // for singleton: true
    cronmap.WithSentry(),                               // for sentry_slug
)
if err != nil { return err }
if err := rt.Start(ctx); err != nil { return err }
defer rt.Stop(ctx)
```

## Lifecycle

```
New → LoadFile/LoadBytes (n) → RegisterHandler (n) → Build (once) → Runtime → Start / Stop
```

После Build engine закрыт — повторные `Load*`/`RegisterHandler`
паникуют (`cronmap_already_built` / `cronmap_already_registered`).

## YAML

| Поле | Required | Default | Что |
|---|---|---|---|
| `name` | да | — | Unique within engine; key для metrics + singleton lock. |
| `handler` | да | — | Имя registered handler'а в Go. |
| `schedule` | да | — | Cron expression. Default parser — 5-field; seconds-precision через [WithParser](#wittparser). |
| `timeout` | нет | 0 (no deadline) | Go duration. Per-run wraps fn в `context.WithTimeout`. |
| `singleton` | нет | false | Если true — требует `WithSingletonLocker` на Build. |
| `sentry_slug` | нет | `slugify(name)` | Передаётся в `sentrykit.MonitorCron` если `WithSentry()`. |
| `max_retries` | нет | 0 (no retry) | Caps re-invocations on err / timeout / panic. Default behaviour — no retry. |
| `retry_backoff` | нет | 0 (no wait) | Initial delay между attempts; doubles per attempt capped at base × 8. |

`${VAR}` substitution работает в любом string-поле (как в apimap/natsmap):

```yaml
schedule: "0 ${DAILY_HOUR} * * *"   # DAILY_HOUR=3 → "0 3 * * *"
```

## Singleton — leader election

`singleton: true` означает "только одна реплика из N запускает этот job на каждом тике". Требует Locker. Kit-default — [PGLocker](#pglocker) через `db/lock`'овский `pg_try_advisory_lock`.

При failed acquire (lock held другой репликой) — runtime ИНКРЕМЕНТИРУЕТ `cronmap_singleton_skipped_total{name}`, **НЕ** `cronmap_runs_total{outcome=failure}`. Это **expected** state в N-1 of N pods — бандлить в "failure" зашумило бы alerting dashboards.

При backend-error от Locker — runtime инкрементит `cronmap_runs_total{outcome=failure}` и логирует. Handler не вызывается.

## API

```go
type HandlerFn func(ctx context.Context) error

type SingletonLocker interface {
    TryLock(ctx context.Context, key string) (release func(), ok bool, err error)
}

// Engine construction
func New(opts ...EngineOption) *Engine
func WithEnv(m map[string]string) EngineOption

// Loading
func (e *Engine) LoadFile(path string) error
func (e *Engine) LoadBytes(b []byte) error

// Handler registration (panics on dup / post-Build)
func RegisterHandler(e *Engine, name string, fn HandlerFn)

// Build
func (e *Engine) Build(opts ...BuildOption) (*Runtime, error)

// BuildOptions
func WithParser(p cron.Parser) BuildOption
func WithLogger(l *slog.Logger) BuildOption
func WithMetrics(r prometheus.Registerer) BuildOption
func WithSingletonLocker(l SingletonLocker) BuildOption
func WithSentry() BuildOption
func WithOnTickStart(fn func(ctx, name string)) BuildOption
func WithOnTickComplete(fn func(ctx, name string, err error, elapsed time.Duration)) BuildOption

// Runtime
func (r *Runtime) Start(ctx context.Context) error
func (r *Runtime) Stop(ctx context.Context) error
func (r *Runtime) JobNames() []string

// /admin endpoints
func (r *Runtime) Stats() []JobStats                              // per-job snapshot
func (r *Runtime) NextRun(name string) (time.Time, error)         // schedule.Next(now)
func (r *Runtime) TriggerJob(ctx context.Context, name string) error  // manual run (bypass singleton + pause)
func (r *Runtime) PauseJob(name string) error                     // skip scheduled ticks
func (r *Runtime) ResumeJob(name string) error                    // re-enable

type JobStats struct {
    Name            string
    Paused          bool
    TotalRuns       int64
    SuccessCount    int64
    FailureCount    int64
    TimeoutCount    int64
    SkippedCount    int64
    LastRunAt       time.Time
    LastOutcome     string
    LastRunDuration time.Duration
    NextRunAt       time.Time
}

// PG-backed Locker (uses db/lock advisory locks)
func PGLocker(d *db.DB) SingletonLocker
```

## Retry policy

```yaml
jobs:
  - name: nightly-rollup
    handler: rollup
    schedule: "0 3 * * *"
    max_retries: 3
    retry_backoff: 30s   # 30s → 60s → 120s (× 2^N, capped at base × 8)
```

Применяется к err / timeout / panic. Успешный retry surface'ит как `success` outcome в metrics. Exhausted retries → `failure` (or `timeout` если последняя попытка дала DeadlineExceeded). Ack-like behaviour — Stats и hooks fire после final attempt.

## Lifecycle hooks

```go
rt, _ := eng.Build(
    cronmap.WithOnTickStart(func(ctx context.Context, name string) {
        span := trace.SpanFromContext(ctx); span.SetAttributes(attribute.String("cron.name", name))
    }),
    cronmap.WithOnTickComplete(func(ctx context.Context, name string, err error, d time.Duration) {
        auditLog.Record(name, statusOf(err), d)
    }),
)
```

Hooks panic-safe (recover + Warn-log). Multiple calls — last wins. `elapsed` — total wall-clock time всех attempts вместе.

## /admin operations

```go
// Snapshot для /admin/cron-status
for _, s := range rt.Stats() {
    fmt.Printf("%s: %d runs, %d failures, next=%s\n",
        s.Name, s.TotalRuns, s.FailureCount, s.NextRunAt)
}

// Operator triggers job out-of-band ("force-run now"):
_ = rt.TriggerJob(ctx, "nightly-rollup") // bypasses singleton + pause

// Operator disables a job temporarily:
_ = rt.PauseJob("nightly-rollup")
// ... fix the upstream, then:
_ = rt.ResumeJob("nightly-rollup")

// Predict next fire-at:
next, _ := rt.NextRun("nightly-rollup")
```

Все эти operations fire metrics + hooks как обычный tick.

## Шаблоны cron expressions

Default parser — **5-field** (no seconds): `min hour dom month dow` плюс `@hourly`/`@daily`/`@weekly`/`@monthly`/`@yearly` descriptors.

Seconds-precision parser через `WithParser`:

```go
parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
rt, _ := eng.Build(cronmap.WithParser(parser))
```

```yaml
schedule: "*/5 * * * * *"   # every 5 seconds (when seconds-parser is used)
```

## Lifecycle и shutdown

`Start(ctx)` — kicks the cron tick goroutines. `ctx` — parent для runtime'овой cancellation цепочки.

`Stop(ctx)` — graceful drain:
1. Cancel runtimeCtx (handlers observably-aware of ctx return early).
2. Stop robfig/cron's tick loop.
3. Wait для in-flight handlers (deadline из `ctx.Deadline()`, default 5s).

`Stop` идемпотентен. После `Stop` runtime sealed — second `Start` возвращает `*errs.Error{cronmap_runtime_stopped}`.

`(*Runtime)(nil)` safe — Start/Stop/JobNames no-op.

## Observability

### slog

| Уровень | Когда |
|---|---|
| `Info "cronmap: scheduler started"` | После Start |
| `Info "cronmap: runtime stopped"` | После завершения Stop, с `drain_ms` |
| `Warn "cronmap: job failed"` | Handler returned err |
| `Warn "cronmap: job timed out"` | DeadlineExceeded из per-run timeout |
| `Debug "cronmap: singleton skipped"` | TryLock returned ok=false |

### Prometheus

| Метрика | Тип | Labels |
|---|---|---|
| `cronmap_runs_total` | Counter | `name`, `outcome` (`success` / `failure` / `timeout`) |
| `cronmap_run_duration_seconds` | Histogram (DefBuckets) | `name` |
| `cronmap_singleton_skipped_total` | Counter | `name` |
| `cronmap_jobs` | Gauge | — (set once at Build) |

## Errors

| Code | Когда |
|---|---|
| `cronmap_missing_name` | YAML job без `name:` |
| `cronmap_missing_handler` | YAML job без `handler:` |
| `cronmap_missing_schedule` | YAML job без `schedule:` |
| `cronmap_invalid_schedule` | Парсер отказался |
| `cronmap_invalid_timeout` | Отрицательный `timeout:` |
| `cronmap_duplicate_job` | Два jobs шарят `name` |
| `cronmap_unknown_handler` | Job ссылается на handler не зарегистрированный через `RegisterHandler` |
| `cronmap_singleton_needs_locker` | `singleton: true` без `WithSingletonLocker` |
| `cronmap_already_built` | Build/Load*/Register* после Build |
| `cronmap_already_registered` | Дубль `RegisterHandler` для name |
| `cronmap_runtime_stopped` | Start после Stop |
| `cronmap_env_var_unset` / `cronmap_env_var_malformed` | ${VAR} substitution failure |

Build агрегирует все validation errors через `errors.Join` — caller видит каждую проблему за один проход.

## Panic recovery

Handler panic → turned into `cronmap_runs_total{outcome=failure}` + `Warn` log. Cron entry остаётся armed на следующий тик (same convention as `service.WithCron`).

## Что НЕ делает

- **Payload-carrying jobs.** Используйте [`db/jobs`](../db/jobs/README.md).
- **In-process serialisation одного job'а.** v2 option; пока используйте `singleton: true` через PGLocker (cross-instance serialisation которая работает в т.ч. и in-process).
- **Adaptive retry.** Failures просто метрикaм + log. Caller-supplied retry wrapper над `HandlerFn` нужен для retry-логики.
- **One-shot scheduled jobs из YAML** (`at: 2026-01-15T03:00:00Z`). Defer to `db/jobs`.

## См. также

- [`service/`](../service/README.md) — `service.WithCron` для ad-hoc jobs программно (в v2 ждём `service.WithCronMap` обёртку над cronmap'ом).
- [`db/lock`](../db/lock/README.md) — primitive за PGLocker'ом.
- [`db/jobs`](../db/jobs/README.md) — payload-carrying delayed-job queue.
- [`sentrykit`](../sentrykit/README.md) — `sentrykit.MonitorCron` обёртка.
