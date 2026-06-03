# breaker

Generic трёхсостоятельный circuit breaker. `breaker.New(cfg)` возвращает
goroutine-safe `*Breaker`, который считает failures в rolling time window'е
и размыкается, когда апстрим перестаёт отвечать — закрывая retry'ям и
caller'ам быстрый path с `ErrOpen` вместо очередного бесполезного round-trip'а.

**Импорт:** `github.com/theizzatbek/gokit/breaker`
**Зависит от:** stdlib + `github.com/prometheus/client_golang/prometheus` (опционально)

## Когда использовать

- Исходящий HTTP (`clients/httpc.WithBreaker(b)` / `clients/apimap` YAML
  `breaker:` блок) — типичный кейс.
- Любая операция против внешней системы, у которой есть бинарное
  "доступна/недоступна" состояние: NATS publish, S3 upload, DB read-replica,
  webhook target.
- Когда retry'и amplify'ят падение апстрима — без breaker'а одна user-request
  превращается в `MaxRetries+1` round-trip'ов на падшем сервере.

Не нужен, если у апстрима нет понятия "временной недоступности" (например,
local-only функции, in-process cache'и).

## Quickstart — Execute

```go
b, err := breaker.New(breaker.Config{
    Name:              "stripe",
    FailureThreshold:  10,                // 10 failures
    MinimumRequests:   20,                //   в окне как минимум 20 запросов
    WindowDuration:    10 * time.Second,
    OpenInterval:      30 * time.Second,
    HalfOpenMaxProbes: 1,
    Logger:            logger,
    Metrics:           promReg,
})
if err != nil { return err }

err = b.Execute(func() error {
    return callStripe(ctx)
})
if errors.Is(err, breaker.ErrOpen) {
    // Short-circuit: Stripe сейчас считается недоступным.
    // Fast-fail без сетевого вызова.
}
```

`Execute` — ergonomic путь: один `Allow + run + done`. Failure определяется
через `Config.IsFailure(err)` (по дефолту "err != nil и не `context.Canceled`").

## Quickstart — Allow (двухфазная форма)

Когда нужно посмотреть на response *до* классификации (классический пример —
HTTP-транспорт, где 200 — success даже если body-decode возвращает err):

```go
allowed, done := b.Allow()
if !allowed {
    return breaker.ErrOpen
}
resp, err := transport.RoundTrip(req)
done(isSuccess(resp, err))  // ваш классификатор
return resp, err
```

`done` гарантированно безопасна для повторного вызова (no-op после первого) и
generation-tagged — если breaker за это время уже переключил состояние,
outcome игнорируется (фиксит race "stale probe answer arrives after re-trip").

## Состояния

```
        threshold reached
closed ─────────────────────► open
  ▲                            │
  │ all probes succeed         │ OpenInterval elapsed
  │                            ▼
  └───── half_open ◄────── (next Allow rotates)
           │
           │ any probe fails
           ▼
         open (new OpenInterval starts)
```

- **closed** — трафик идёт; failures копятся в rolling window'е. Когда
  `failures ≥ FailureThreshold` И `requests ≥ MinimumRequests` — переход в **open**.
- **open** — каждый `Allow` возвращает `(false, noop)` и инкрементит
  `breaker_short_circuits_total`. По истечении `OpenInterval` следующий `Allow`
  поднимает breaker в **half_open**.
- **half_open** — пропускаются ровно `HalfOpenMaxProbes` параллельных проб.
  ВСЕ должны вернуть success, чтобы вернуться в **closed**. Первая же
  failure-проба роняет обратно в **open** со свежим `OpenInterval`.

## Config

| Поле | По умолчанию | Заметки |
|---|---|---|
| `Name` | (обязательно) | `name`-label на каждой `breaker_*` серии. |
| `FailureThreshold` | 10 | Сколько failure'ов в окне триггерят open. |
| `MinimumRequests` | 20 | Минимум total-requests, прежде чем breaker может открыться. Валидируется `MinimumRequests ≥ FailureThreshold` (иначе trip-условие недостижимо). Дефолт = "50% failure rate over ≥20 calls". |
| `WindowDuration` | 10s | Полный span rolling-окна. |
| `WindowSize` | 10 | Кол-во bucket'ов окна. 10 bucket'ов × 1s каждый — баланс между smooth roll-off и памятью. |
| `OpenInterval` | 30s | Длительность open-фазы. Константа по всем re-trip'ам (adaptive — v2). |
| `HalfOpenMaxProbes` | 1 | Сколько параллельных проб допускается. Все должны успеть. |
| `IsFailure` | `err != nil && !errors.Is(err, context.Canceled)` | Классификатор. `context.Canceled` НЕ считается failure'ом (user cancel ≠ upstream down); `DeadlineExceeded` СЧИТАЕТСЯ (это и есть slow upstream). |
| `Now` | `time.Now` | Инъекция clock'а для тестов. |
| `Logger` | nil (silent) | Info на каждом state-transition. |
| `Metrics` | nil (без коллекторов) | Регистрирует четыре breaker_* серии. |

## API

```go
type State int
const (
    StateClosed   State = 0
    StateOpen     State = 1
    StateHalfOpen State = 2
)

func (s State) String() string  // "closed" / "open" / "half_open" / "unknown"

func New(cfg Config) (*Breaker, error)
func (b *Breaker) State() State
func (b *Breaker) Allow() (allowed bool, done func(success bool))
func (b *Breaker) Execute(fn func() error) error
func (b *Breaker) Stats() Stats              // cheap snapshot for /admin
func (b *Breaker) ForceOpen(d time.Duration) // operator override
func (b *Breaker) ForceClose()               // operator override

var ErrOpen = errors.New("breaker: circuit open")
```

### Adaptive `OpenInterval`

```go
Config{
    OpenInterval:           10 * time.Second,
    OpenIntervalMultiplier: 3.0,             // 10s → 30s → 90s → ...
    OpenIntervalMax:        5 * time.Minute, // cap
}
```

Каждый последовательный re-trip (без успешного close между ними) увеличивает effective open duration в `Multiplier` раз, до `OpenIntervalMax`. Successful close сбрасывает счётчик — следующий fresh trip начинает с base `OpenInterval`. По умолчанию `Multiplier=1.0` (back-compat — константный interval).

### `HalfOpenSuccessThreshold` (K of N)

Сейчас по умолчанию ALL `HalfOpenMaxProbes` должны succeed. Для шумных upstream'ов:

```go
Config{
    HalfOpenMaxProbes:        5,
    HalfOpenSuccessThreshold: 3, // 3 of 5 probes ok → close
}
```

Любой failure всё ещё rotates обратно в open независимо от running success counter.

### `OnStateChange` hook

```go
Config{
    OnStateChange: func(from, to breaker.State) {
        slack.Notify("breaker '%s' %s → %s", cfg.Name, from, to)
    },
}
```

Fires внутри breaker mutex AFTER каждого перехода. Panic-safe — kit recover'ит panic'и из callback'а.

### Operator override: `ForceOpen` / `ForceClose`

```go
b.ForceOpen(30 * time.Minute) // maintenance window
// ...
b.ForceClose() // manual reset after incident resolved
```

`ForceOpen(d)` jumps to open и holds it under supplied window (overrides adaptive curve). `ForceClose()` jumps to closed, clears window + counters. Оба fire transition hooks + metrics.

### `Stats()` snapshot

```go
s := b.Stats()
// Stats{State, Generation, WindowRequests, WindowFailures,
//       HalfOpenInFlight, HalfOpenSucceeded, OpenedAt, RemainingOpen,
//       ConsecutiveTrips, CurrentOpenInterval, ForcedOpenUntil}
```

Cheap (one mu acquire). Suitable для /admin или /healthz endpoints. Nil-receiver returns zero value.

`(*Breaker)(nil)` — safe no-op receiver: `Allow` всегда permit'ит, `Execute`
просто запускает `fn`, `State` возвращает `StateClosed`. Это даёт callsite'ам
писать `b.Execute(...)` без nil-check'ов в случае, когда breaker опциональный.

## Observability

### slog

`Logger.Info("breaker state transition", "name", ..., "from", ..., "to", ...)`
эмитится на каждом переходе. Без переходов — silent.

### Prometheus

Когда `Metrics != nil`:

| Метрика | Тип | Labels |
|---|---|---|
| `breaker_state` | Gauge | `name` (значение 0/1/2 = closed/open/half_open) |
| `breaker_transitions_total` | Counter | `name`, `from`, `to` |
| `breaker_short_circuits_total` | Counter | `name` |
| `breaker_requests_total` | Counter | `name`, `outcome` (`success` / `failure` / `short_circuit`) |

`name` — `ConstLabel`, так что один Prometheus-registry может держать N
breaker'ов без коллизий — `clients/apimap` точно так и делает (один breaker на
client name).

## Error-модель

`breaker/` намеренно не зависит от `errs/` — пакет остаётся
stdlib-only. Адаптеры (`clients/httpc`, кит-уровневые wiring'и) оборачивают:

- `ErrOpen` — sentinel для short-circuit'а; `errors.Is(err, breaker.ErrOpen)` после wrapping'а.
- Локальный `breaker.Error{Code, Message}` для config-validation'а в `New`:
  `breaker_invalid_name`, `breaker_invalid_failure_threshold`,
  `breaker_invalid_minimum_requests`, `breaker_invalid_window`,
  `breaker_invalid_open_interval`, `breaker_invalid_half_open_max_probes`.

## Тестирование

`Config.Now` — инъекция clock'а. Стандартный паттерн:

```go
type fakeClock struct{ mu sync.Mutex; now time.Time }
func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.now = c.now.Add(d) }

clk := &fakeClock{now: time.Unix(1_700_000_000, 0)}
b, _ := breaker.New(breaker.Config{Name: "test", Now: clk.Now, /* ... */})

// Trip:
for i := 0; i < cfg.FailureThreshold; i++ { _ = b.Execute(func() error { return errBoom }) }

// Wait past OpenInterval:
clk.Advance(cfg.OpenInterval + time.Second)

// Probe success closes:
_ = b.Execute(func() error { return nil })
// b.State() == StateClosed
```

## Ограничения / out-of-scope

- **Adaptive `OpenInterval`** на re-trip — v2. Сейчас интервал константный для
  стабильной формы `breaker_open_duration_seconds`.
- **Ratio-based threshold** ("50% of last 100 requests fail") — v1 использует
  абсолютные counts. Добавится как XOR-альтернатива, если появится консумер.
- **Per-host внутри одного breaker'а** (когда один транспорт ходит к
  разным хостам через тот же breaker). Пока решается на уровне caller'а:
  один breaker на host.
- **Recovery panic'ов в `Execute`** не делается — `fn` panic'ит → done НЕ
  вызывается → defer-цепочка caller'а отрабатывает обычным путём. Хотите
  "panic = failure" — оборачивайте `fn` сами.

## См. также

- [`clients/httpc`](../clients/httpc/README.md) — основной consumer (`WithBreaker(b)`).
- [`clients/apimap`](../clients/apimap/README.md) — декларативный YAML
  `breaker:` блок per client.
- [`errs`](../errs/README.md) — error-контракт kit'а (adapter'ы оборачивают
  `ErrOpen` в `*errs.Error{KindUnavailable, Code: "..._circuit_open"}`).
