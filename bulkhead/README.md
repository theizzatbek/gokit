# bulkhead

Generic concurrency-cap с bounded wait queue. `bulkhead.New(cfg)` возвращает
goroutine-safe `*Bulkhead`, который ограничивает число одновременных вызовов
к одному апстриму и опционально queue'ит небольшое количество waiter'ов.
Защищает worker-пул от того, что один медленный API съест все горутины.

**Импорт:** `github.com/theizzatbek/gokit/bulkhead`
**Зависит от:** stdlib + `github.com/prometheus/client_golang/prometheus` (опционально)

## Когда использовать

- Исходящий HTTP (`clients/httpc.WithBulkhead(b)` / `clients/apimap` YAML
  `bulkhead:` блок) — типичный кейс.
- Любая операция против внешней системы, которая может "залипать" на минуты
  не возвращая ошибку (medium-down упрям, DB-read-replica с lag-failover,
  S3 stuck connection).
- Когда `breaker/` НЕ покрывает кейс: апстрим живой, но медленный — failures
  не накапливаются, breaker не размыкается, но каждая ваша горутина сидит в
  `RoundTrip` минутами и пул заканчивается.

Не нужен, если у апстрима fast-fail семантика — там breaker'а достаточно.

## Bulkhead vs Breaker vs Rate-limit

| | Breaker | Bulkhead | Rate-limit |
|---|---|---|---|
| Что считает | Failures over time | Concurrent in-flight | RPS over time |
| Что защищает | "Апстрим down — не долбить" | "Один апстрим не сожрёт worker-пул" | "Не превысить cap, согласованный с апстримом" |
| Срабатывает по | Error rate ≥ threshold | Slot occupancy = MaxConcurrent | Tokens exhausted in window |
| Решает | Cascading failure | Goroutine wedge | Throttling / quota |

Они ортогональны и часто стоят в одной цепочке. apimap позволяет включить
оба `breaker:` и `bulkhead:` блока для одного и того же клиента.

## Quickstart — Execute

```go
b, err := bulkhead.New(bulkhead.Config{
    Name:          "stripe",
    MaxConcurrent: 20,                  // макс. 20 одновременных
    MaxQueue:      50,                  // ещё 50 могут ждать
    QueueTimeout:  100 * time.Millisecond, // wait не дольше 100ms
    Metrics:       promReg,
})
if err != nil { return err }

err = b.Execute(ctx, func() error {
    return callStripe(ctx)
})
switch {
case errors.Is(err, bulkhead.ErrBulkheadFull):
    // 503-ish fast-fail — Stripe сейчас saturated, шлите на fallback.
case errors.Is(err, bulkhead.ErrQueueTimeout):
    // Прождал больше QueueTimeout — апстрим перегружен.
case errors.Is(err, context.Canceled):
    // Caller отменил wait.
case err != nil:
    // Это была ошибка от callStripe — slot уже освобождён.
}
```

## Quickstart — Acquire (двухфазная форма)

Когда нужно вручную контролировать lifecycle slot'а:

```go
release, err := b.Acquire(ctx)
if err != nil { return err }
defer release()

resp, err := transport.RoundTrip(req)
return resp, err
```

`release` идемпотентна (повторный вызов — no-op через `atomic.Bool`), поэтому
double-defer в обёртках безопасен.

## Config

| Поле | Required | По умолчанию | Заметки |
|---|---|---|---|
| `Name` | да | — | `name`-label на каждой `bulkhead_*` серии. |
| `MaxConcurrent` | да | — | Жёсткий cap на slots. Должен быть > 0. |
| `MaxQueue` | нет | 0 (fail-fast) | Сколько callers'ов может ждать когда slots заняты. `-1` (unlimited) НЕ поддерживается — это и есть failure mode, который мы предотвращаем. |
| `QueueTimeout` | нет | 0 (только caller ctx) | Bounds wait в очереди даже если caller'у ctx разрешает больше. Полезно для "fail-fast → fallback" паттернов. |
| `Logger` | нет | nil (silent) | Reserved для будущих state-change records. |
| `Metrics` | нет | nil | Регистрирует четыре bulkhead_* серии. |

## API

```go
func New(cfg Config) (*Bulkhead, error)

// Acquire blocks (with ctx + optional QueueTimeout) until a slot frees,
// the queue cap is exceeded (ErrBulkheadFull), QueueTimeout fires
// (ErrQueueTimeout), or ctx is cancelled (ctx.Err()).
func (b *Bulkhead) Acquire(ctx context.Context) (release func(), err error)

// Execute is the ergonomic wrapper: Acquire + run + release.
func (b *Bulkhead) Execute(ctx context.Context, fn func() error) error

// Stats returns the cheap point-in-time snapshot (InFlight, Waiting,
// Capacity) plus rolling LatencyP50 / LatencyP99 / AvgWait /
// SampleSize aggregates over Config.StatsWindow (default 10s).
type Stats struct {
    InFlight   int
    Waiting    int
    Capacity   int
    LatencyP50 time.Duration
    LatencyP99 time.Duration
    AvgWait    time.Duration
    SampleSize int
}
func (b *Bulkhead) Stats() Stats

// SetCapacity is the operator runbook lever; fires OnCapacityChange
// when prev != next.
func (b *Bulkhead) SetCapacity(n int)

var ErrBulkheadFull = errors.New("bulkhead: full")
var ErrQueueTimeout = errors.New("bulkhead: queue wait timeout")
```

### `OnCapacityChange` hook

```go
Config{
    OnCapacityChange: func(prev, next int) {
        slack.Notify("bulkhead '%s': capacity %d → %d", cfg.Name, prev, next)
    },
}
```

Fires AFTER `SetCapacity` (manual или adaptive tick) applies a non-trivial change. No-op SetCapacity (same value) suppresses the callback. Panic-safe.

### `OnAcquireFail` hook

```go
Config{
    OnAcquireFail: func(reason string) {
        // reason ∈ {"full", "ctx_canceled", "queue_timeout"} — same
        // labels as bulkhead_acquire_total{outcome=...}
        log.Warn("bulkhead rejection", "name", cfg.Name, "reason", reason)
    },
}
```

Fires на каждом reject path Acquire/Execute (полный bulkhead, ctx cancel в очереди, queue timeout). Symmetric to `OnCapacityChange`. Pair with `bulkhead_acquire_total{outcome=…}` метрикой когда хотите доменный сигнал (mark upstream sick, switch to fallback) из того же события что и метрики. Panic-safe.

### Enhanced `Stats()`

`Stats()` теперь включает rolling latency + wait aggregates над `Config.StatsWindow` (default 10s):

- `LatencyP50` / `LatencyP99` — p50/p99 in-flight call duration.
- `AvgWait` — average queue wait time across all Acquires.
- `SampleSize` — total observations within the window (для "no data" check).

Cheap (one mu acquire + small slice scan; ring buffer capped at 4096 entries). Suitable для /healthz / capacity planning dashboards.

`(*Bulkhead)(nil)` — safe no-op receiver: `Acquire` всегда permit'ит,
`Execute` запускает `fn`, `Stats` возвращает zero value. Лет callsite'ам
писать `b.Execute(...)` без nil-check'ов.

## Слот = время сетевого round-trip'а, НЕ время чтения body

В httpc-адаптере `release()` срабатывает СРАЗУ после возврата `base.RoundTrip`,
т.е. ДО того, как caller прочитает response body. Это intentional:

- Body streaming может занимать минуты (большой JSON, multi-part upload).
- Держать slot всё это время = превратить bulkhead в "concurrency cap по
  total request lifetime", что неправильно — медленный JSON-decoder заблокирует
  новые requests при здоровом upstream'е.
- Goroutine-уровневую concurrency (читающий body caller) контролируете вы
  на handler-уровне, не bulkhead.

## Fairness

`Acquire` НЕ гарантирует FIFO. Go's `select` рандомизирует winner'а среди
ready cases. На практике это означает, что в high-contention сценарии waiter
может ждать дольше, чем "его номер в очереди" подразумевает. Если строгая
fairness критична, оберните `bulkhead` собственной FIFO-очередью.

## Observability

### Prometheus (когда `Metrics != nil`)

| Метрика | Тип | Labels |
|---|---|---|
| `bulkhead_in_flight` | Gauge | `name` (snapshot on scrape) |
| `bulkhead_waiting` | Gauge | `name` (snapshot on scrape) |
| `bulkhead_capacity` | Gauge | `name` (current MaxConcurrent — двигается при WithAdaptive) |
| `bulkhead_acquires_total` | Counter | `name`, `outcome` (`ok` / `full` / `ctx_canceled` / `queue_timeout`) |
| `bulkhead_wait_duration_seconds` | Histogram (DefBuckets) | `name`, `outcome` |
| `bulkhead_call_latency_seconds` | Histogram (DefBuckets) | `name` (release - Acquire — для AIMD/Vegas controller'ов) |

`name` — `ConstLabel`, так что один Prometheus registry держит N bulkhead'ов
без коллизий — `clients/apimap` точно так и делает (один bulkhead на client name).

### Dashboard cookbook

- **"Bulkhead saturated"** alert: `rate(bulkhead_acquires_total{outcome="full"}[5m]) > 0`
  означает "capacity слишком низкая ИЛИ апстрим медлит сильнее, чем рассчитано".
- **"Queue building up"** signal: `bulkhead_waiting` вырастает над typical
  baseline — апстрим начинает деградировать раньше, чем сорвётся в `full`.
- **"Wait p99 spikes"**: `histogram_quantile(0.99, bulkhead_wait_duration_seconds)` —
  ранний индикатор upstream-latency creep.

## Error-модель

`bulkhead/` намеренно не зависит от `errs/` — пакет остаётся
stdlib-only. Адаптеры (`clients/httpc`, кит-уровневые wiring'и) оборачивают:

- `ErrBulkheadFull` — sentinel queue saturation; `errors.Is(...)` работает после wrapping'а.
- `ErrQueueTimeout` — sentinel истёкшего `QueueTimeout`.
- Локальный `bulkhead.Error{Code, Message}` для config-validation в `New`:
  `bulkhead_invalid_name`, `bulkhead_invalid_max_concurrent`,
  `bulkhead_invalid_max_queue`, `bulkhead_invalid_queue_timeout`.

## Тестирование

`Acquire`/`Release` синхронны и не используют `time.Now` напрямую (только
`time.NewTimer` для `QueueTimeout`). Стандартные unit-тесты без injection clock'а:

```go
b, _ := bulkhead.New(bulkhead.Config{MaxConcurrent: 1, MaxQueue: 0, Name: "test"})

r, err := b.Acquire(context.Background())
// ...
_, err = b.Acquire(context.Background())
if !errors.Is(err, bulkhead.ErrBulkheadFull) { t.Fatal(err) }
r()
```

`Stats()` — cheap, можно опрашивать в spinloop'е в тестах:

```go
for b.Stats().Waiting < 2 { time.Sleep(time.Millisecond) }
```

## Adaptive concurrency (auto-tuning MaxConcurrent)

Жёсткий `MaxConcurrent` — это guesswork: слишком тесно → недоиспользуем upstream
в здоровом state'е; слишком свободно → backed-up latency каскадирует. Опция
`WithAdaptive` запускает controller-loop, который двигает cap по observable
upstream pressure.

```go
b, _ := bulkhead.New(bulkhead.Config{Name: "stripe", MaxQueue: 100},
    bulkhead.WithAdaptive(bulkhead.AdaptiveConfig{
        Controller:   &bulkhead.AIMDController{
            IncreaseStep:   1,    // +1 на healthy tick
            DecreaseFactor: 0.5,  // ÷2 при error spike
            ErrorThreshold: 0.1,  // 10% error rate trip
        },
        InitialCap:   10,
        MinCapacity:  2,
        MaxCapacity:  100,
        TickInterval: 1 * time.Second,
        WindowSize:   10 * time.Second,
    }),
)
defer b.Close()   // adaptive mode ОБЯЗАН быть Closed
```

**`Config.MaxConcurrent` НЕ ставится** при `WithAdaptive` — capacity владеет
adaptive layer. Validation падает с `apimap_invalid_adaptive_config` если
указаны оба.

### Controller interface

Plug-and-play algorithm:

```go
type Controller interface {
    Next(s Snapshot) int
}

type Snapshot struct {
    Capacity   int
    InFlight   int
    Waiting    int
    Latency    LatencyStats   // p50, p99, count over WindowSize
    ErrorRate  float64        // 0.0–1.0 over WindowSize
    SinceLast  time.Duration
}
```

Шипятся две реализации `Controller`:

**`AIMDController`** — additive-increase / multiplicative-decrease (TCP-style):
- `Latency.Count == 0` (no traffic) → hold capacity. Защищает от unintended
  shrink при open-circuit-period.
- `ErrorRate ≥ ErrorThreshold` → `cap × DecreaseFactor`, floor 1.
- Иначе → `cap + IncreaseStep`.

**`VegasController`** — TCP-Vegas-inspired, latency-aware. Запоминает минимальный наблюдённый `P50` как baseline (proxy на "propagation delay") и оценивает queue length по отношению `current P50 / baseline`:
- `Latency.Count == 0` → hold (та же конвенция, что и AIMD).
- `ErrorRate ≥ ErrorThreshold` → `cap / 2`, floor 1 (TCP Vegas сам по себе не реагирует на loss-style сигналы; в service-mesh контексте трактуем 5xx + cancellation как packet loss).
- `queueSize = capacity - capacity * baseline / P50`:
  - `queueSize < Alpha` (default 2) → `cap + 1` (есть headroom — push).
  - `queueSize > Beta` (default 6) → `cap - 1` (началось queueing — pull back).
  - Между Alpha и Beta — hold (sweet spot).

Когда выбирать что: AIMD реагирует только на ошибки и monotonically растёт между ними — хорош, если downstream хорошо различает success/failure и latency не главный сигнал. Vegas реагирует на latency raise задолго до того, как downstream начнёт фейлиться — лучше для случаев, когда нагрузка деградирует постепенно (DB connection pool exhaustion, slow downstream без 5xx).

Расширение: `Controller` — open extension point. Gradient2 или другие алгоритмы могут жить за тем же interface'ом — write a struct with a single `Next(Snapshot) int` method.

### Error rate источник

`Execute(ctx, fn)` подаёт `fn err == nil` в latency window как success
outcome. То есть AIMD автоматически видит fn-failures без caller bookkeeping.
Двухфазная `Acquire()` форма дефолтит к success=true — если хотите feedback
от error'ов, используйте Execute.

### SetCapacity (manual lever)

`b.SetCapacity(n)` — operator runbook lever. "Stripe в инциденте, force cap
до 5 на 10 минут." Та же primitive, что adaptive внутри. Raising cap'а
просыпает waiter'ов через `cond.Broadcast`; lowering НЕ preempt'ит in-flight
(drain on shrink). Adaptive tick может перезаписать руками выставленную cap
на следующем тике — для строгого override отключите adaptive.

### Drain on shrink

Уменьшение capacity ниже in-flight count НЕ прерывает текущие slots — они
завершатся естественно. Новые Acquire'ы блокируются пока `inflight ≤ capacity`
не выполнится.

## Ограничения / out-of-scope

- **Priority queue** — bulkhead не различает background-vs-user requests.
- **Per-host внутри одного bulkhead'а** — для multi-host клиента используйте
  отдельные bulkhead'ы.
- **Saga / cross-bulkhead coordination** — bulkhead локален, ничего не знает
  про друзей.
- **Manual ops (drain / pause)** — для maintenance window'а нужен отдельный
  механизм, bulkhead не делает SRE-уровневую блокировку.

## См. также

- [`breaker`](../breaker/README.md) — ортогональный resilience-pattern
  (error-rate trip vs concurrency cap). Бывают в одной цепочке.
- [`clients/httpc`](../clients/httpc/README.md) — основной consumer
  (`WithBulkhead(b)`).
- [`clients/apimap`](../clients/apimap/README.md) — декларативный YAML
  `bulkhead:` блок per client.
- [`clients/ratelimit`](../clients/ratelimit/README.md) — RPS-throttling
  (другая ось защиты).
