# clients/httpc

Builder исходящего HTTP-клиента. `httpc.New(cfg, opts...)` возвращает stdlib-овский `*http.Client`, transport-цепочка которого оборачивает caller-supplied или `http.DefaultTransport` per-attempt таймаутом, full-jitter exponential retry'ями на транзиентные failures и опциональной slog/Prometheus observability. Возвращает stdlib-овский `*http.Client`, так что компонуется с любой библиотекой, которая хочет этот тип (AWS SDK, Stripe, OAuth-библиотеки, …).

**Импорт:** `github.com/theizzatbek/gokit/clients/httpc`
**Зависит от:** stdlib + `prometheus/client_golang` + `github.com/theizzatbek/gokit/errs`

## Зачем это нужно

Boilerplate исходящего HTTP одинаков в каждом сервисе: `*http.Client` с `Timeout`-границами, retry с backoff'ом на транзиентных failures, чтить `Retry-After`, логировать на правильном уровне, экспонировать метрики. `httpc` — это такой бандл, выставленный через `Config` + функциональные опции. Возвращает стандартный `*http.Client`, так что любой SDK, принимающий такой, работает без изменений.

## Quickstart

```go
import (
    "time"
    "github.com/theizzatbek/gokit/clients/httpc"
)

c, err := httpc.New(httpc.Config{
    Timeout:     10 * time.Second,
    MaxRetries:  3,
    BackoffBase: 100 * time.Millisecond,
    BackoffMax:  5 * time.Second,
}, httpc.WithLogger(logger), httpc.WithMetrics(promReg))
if err != nil { return err }

resp, err := c.Get("https://api.example.com/users/42")
```

Всё работает так, будто это `http.DefaultClient` — retry'и происходят прозрачно для идемпотентных методов на транзиентных failures; response — обычный `*http.Response`.

## Конфигурация

### `httpc.Config`

| Поле | По умолчанию | Заметки |
|---|---|---|
| `Timeout` | 10s | Per-attempt deadline (через `context.WithTimeout`). Wall-clock budget по всем retry'ям — это deadline `req.Context()` caller'а. |
| `MaxRetries` | 3 (когда не указан) | Количество *дополнительных* попыток после первой. Передайте `-1`, чтобы выключить retry'и полностью. |
| `BackoffBase` | 100ms | Начальная экспоненциальная задержка. Jitter — `rand.Float64() * min(base * 2^attempt, max)` |
| `BackoffMax` | 5s | Cap на exponential рост |

### Опции

| Опция | По умолчанию | Заметки |
|---|---|---|
| `WithLogger(*slog.Logger)` | silent | Debug на каждом retry-решении, Warn на исчерпании retry'ев |
| `WithMetrics(prometheus.Registerer)` | нет коллекторов | Регистрирует requests_total / request_duration_seconds / retries_total / retries_exhausted_total |
| `WithBaseTransport(http.RoundTripper)` | `http.DefaultTransport` | Override низа цепочки — кладите otel-instrumented или auth-injecting RoundTripper'ы под retry-логику |
| `WithBreaker(*breaker.Breaker)` | nil (выключен) | Circuit breaker между retry и base. См. секцию ниже. |
| `WithBreakerFailureClassifier(fn)` | 408/429/5xx + non-Canceled err | Override "что считается failure'ом" для breaker'а. No-op без `WithBreaker`. |
| `WithBulkhead(*bulkhead.Bulkhead)` | nil (выключен) | Concurrency-cap между retry и breaker. См. секцию ниже. |

## Retry-семантика (hard-coded — no overrides)

- **Только идемпотентные методы:** GET, HEAD, PUT, DELETE, OPTIONS ретраятся. POST и PATCH возвращают после attempt 0 — никогда молча не double-write'ят.
- **Retryable статусы:** 408, 429, 500, 502, 503, 504. Всё остальное (включая 4xx) возвращает немедленно.
- **Network ошибки:** любая ошибка из inner `RoundTrip` (DNS failure, connect refused, EOF mid-stream) ретраится.
- **Backoff:** `delay = rand.Float64() * min(BackoffBase * 2^attempt, BackoffMax)`. Full jitter — минимизирует thundering herd.
- **`Retry-After`:** парсится (integer seconds или HTTP-date). Если присутствует, используется вместо jittered backoff'а, capped at `4 * BackoffMax`.
- **Body replay:** только когда `req.GetBody != nil`. `http.NewRequest` с `bytes.Reader`/`bytes.Buffer`/`strings.Reader` устанавливает его автоматически. Streaming-body (manually-constructed `Request{Body: …}`) пропускают retry после attempt 0.
- **Context cancellation:** preempt'ит и попытки, И backoff-sleep'ы.
- **Exhausted retries:** возвращают последние `(resp, err)` as-is. Caller видит стандартный `*http.Response` (или stdlib-овскую `net.Error`), не `*errs.Error`. Метрика `httpc_retries_exhausted_total` инкрементится.

## Common patterns

### Cancellable per-call timeout

`Config.Timeout` — per-attempt. Для total budget по retry'ям используйте `context.WithTimeout` на call-site:

```go
ctx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
defer cancel()
req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
resp, err := c.Do(req)
```

### Custom base transport (otel, auth)

```go
// Самый внешний: otel instrumentation
// Средний:    httpc retry/timeout
// Самый внутренний: auth header injection
auth := authRoundTripper{token: token}
base := otelhttp.NewTransport(auth, otelhttp.WithSpanNameFormatter(...))

c, _ := httpc.New(httpc.Config{Timeout: 5*time.Second, MaxRetries: 2},
    httpc.WithBaseTransport(base),
)
// Или возьмите только transport для embedding'а в свой *http.Client:
rt, _ := httpc.NewTransport(cfg, httpc.WithBaseTransport(base))
myClient := &http.Client{Transport: rt}
```

### Отключение retry'ев

```go
httpc.New(httpc.Config{Timeout: 5*time.Second, MaxRetries: -1})
```

`-1` — sentinel для "no retries — single attempt only". Zero value (`0`) дефолтится в 3, потому что самая частая ошибка — забыть его установить; opt out явно через `-1`.

### Drop-in для SDK'ов

Всё, что принимает `*http.Client`, работает:

```go
c, _ := httpc.New(httpc.Config{...})
s3 := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
    o.HTTPClient = c
})
```

### Circuit breaker

Когда апстрим падает по-настоящему (не флэкает), retry'и amplify'ят пожар:
одна user-request × `MaxRetries+1` × `BackoffMax` секунд впустую. `WithBreaker`
вставляет [`breaker`](../../breaker/README.md)-слой *под* retry — каждая попытка
консультируется с ним отдельно, и когда breaker открывается, retry-loop сразу
бэйлит с `breaker.ErrOpen` (никакого backoff, никакого нового round-trip'а):

```go
import "github.com/theizzatbek/gokit/breaker"

b, _ := breaker.New(breaker.Config{
    Name:              "stripe",
    FailureThreshold:  10,
    MinimumRequests:   20,
    WindowDuration:    10 * time.Second,
    OpenInterval:      30 * time.Second,
    HalfOpenMaxProbes: 1,
})

c, _ := httpc.New(httpc.Config{Timeout: 10*time.Second, MaxRetries: 3},
    httpc.WithBreaker(b),
)

resp, err := c.Get("https://api.stripe.com/...")
if errors.Is(err, breaker.ErrOpen) {
    // 503-like short-circuit: stripe сейчас считается down,
    // запрос не был отправлен в сеть.
}
```

**Failure классификация** (что инкрементит breaker'у failure-счётчик):

- Дефолт: response с status `{408, 429, 500, 502, 503, 504}` — failure; ошибка от
  base.RoundTrip — failure, **кроме** `context.Canceled` (browser close ≠
  upstream down). `context.DeadlineExceeded` ЕСТЬ failure (это и есть симптом
  медленного апстрима).
- 4xx (404/401/...) НЕ считаются failure'ами — это ошибки клиента, не апстрима.
- Override через `WithBreakerFailureClassifier(func(*http.Response, error) bool)`
  для апстримов с "200 + body `{error: ...}`" семантикой.

**Surface ошибки**: short-circuit'нутый запрос возвращает
`*errs.Error{KindUnavailable, Code: "httpc_circuit_open"}` с `Cause =
breaker.ErrOpen`. Так `errors.Is(err, breaker.ErrOpen)` работает, и
`errs.HTTP(err)` даёт честный 503.

**Шарить один breaker между двумя клиентами** (например, один upstream с
разными httpc-конфигами per endpoint — это паттерн apimap'а):

```go
b, _ := breaker.New(cfg)
fastClient, _ := httpc.New(httpc.Config{Timeout: 1*time.Second}, httpc.WithBreaker(b))
slowClient, _ := httpc.New(httpc.Config{Timeout: 30*time.Second}, httpc.WithBreaker(b))
// Один failure-счётчик на оба клиента — unit-of-failure = upstream.
```

См. [`breaker`](../../breaker/README.md) для конфига, состояний и observability.

### Bulkhead (concurrency cap)

Ортогональный resilience-pattern: breaker ловит "апстрим down", bulkhead —
"апстрим жив но настолько медленный, что мои горутины кончаются". `WithBulkhead`
вставляет [`bulkhead`](../../bulkhead/README.md)-слой ВЫШЕ breaker'а (так
open-circuit не занимает слот) и НИЖЕ retry (каждая попытка acquires
независимо, не камп'ит на слоте через backoff):

```go
import "github.com/theizzatbek/gokit/bulkhead"

bh, _ := bulkhead.New(bulkhead.Config{
    Name:          "stripe",
    MaxConcurrent: 20,
    MaxQueue:      50,
    QueueTimeout:  100 * time.Millisecond,
})

c, _ := httpc.New(httpc.Config{Timeout: 10*time.Second, MaxRetries: 3},
    httpc.WithBulkhead(bh),
)

resp, err := c.Get("https://api.stripe.com/...")
switch {
case errors.Is(err, bulkhead.ErrBulkheadFull):
    // 503 fast-fail: Stripe сейчас перегружен (или ваш cap слишком тугой).
case errors.Is(err, bulkhead.ErrQueueTimeout):
    // 504: прождал больше QueueTimeout — фоллбэк лучше, чем продолжать.
}
```

**Слот = время `base.RoundTrip`'а**, не время чтения body — `release()`
срабатывает на `defer` ДО того, как caller прочитает response body. Медленный
JSON-decoder не блокирует новые requests.

**Retry**: `ErrBulkheadFull` non-retryable (queue уже saturated; retry сделает
хуже). `ErrQueueTimeout` — normal transient (следующий attempt может
проскочить).

**Shared bulkhead** между несколькими `*http.Client`'ами — паттерн apimap'а
(один upstream, разные httpc-настройки per endpoint, один bulkhead):

```go
bh, _ := bulkhead.New(cfg)
fastClient, _ := httpc.New(httpc.Config{Timeout: 1*time.Second}, httpc.WithBulkhead(bh))
slowClient, _ := httpc.New(httpc.Config{Timeout: 30*time.Second}, httpc.WithBulkhead(bh))
```

См. [`bulkhead`](../../bulkhead/README.md) для конфига и observability.

## Error-модель

`*errs.Error` только на валидации конфигурации в `New`/`NewTransport`:

| Code | Когда |
|---|---|
| `httpc_invalid_timeout` | `Timeout < 0` |
| `httpc_invalid_max_retries` | `MaxRetries < -1` |
| `httpc_invalid_backoff` | `BackoffBase` / `BackoffMax` невалидны или `BackoffMax < BackoffBase` |
| `httpc_circuit_open` | Запрос short-circuit'нут открытым breaker'ом из `WithBreaker`. Cause = `breaker.ErrOpen`; `errs.HTTP` → 503 Unavailable. |
| `httpc_bulkhead_full` | Bulkhead saturated (in-flight + queue cap exceeded). Cause = `bulkhead.ErrBulkheadFull`; `errs.HTTP` → 503 Unavailable. |
| `httpc_bulkhead_queue_timeout` | `QueueTimeout` сработал прежде, чем освободился слот. Cause = `bulkhead.ErrQueueTimeout`; `errs.HTTP` → 504 Timeout. |

Runtime-ошибки — stdlib (`*url.Error`, `net.Error` и т.д.) — это и есть *смысл* возврата `*http.Client`. Если ваш handler хочет конвертировать "retry exhausted on 503" в domain-ошибку, оборачивайте руками:

```go
resp, err := c.Get(url)
if err != nil { return errs.Wrap(err, errs.KindUnavailable, "upstream_down", "upstream HTTP call failed") }
if resp.StatusCode >= 500 {
    return errs.Internalf("upstream_5xx", "upstream returned %d", resp.StatusCode)
}
```

## Observability

### slog

- `Debug "httpc retry"` — на каждое retry-решение: `method`, `url`, `attempt`, `delay_ms`, `status`/`err`/`reason="retry_after"`
- `Warn "httpc retries exhausted"` — в конце исчерпанных попыток

Успешные ответы НЕ логируются (это работа otel).

### Prometheus (опционально через `WithMetrics`)

| Метрика | Тип | Labels |
|---|---|---|
| `httpc_requests_total` | counter | `method`, `status` (status="error" для network-failures) |
| `httpc_request_duration_seconds` | histogram (DefBuckets) | `method`, `status` |
| `httpc_retries_total` | counter | `method`, `classification` (`5xx`/`429`/`408`/`network`/`retry_after`) |
| `httpc_retries_exhausted_total` | counter | `method` |

`path` намеренно опущен — high-cardinality. Оберните своё per-endpoint middleware, если нужно.

## Почему net/http, а не fasthttp?

Хотя fiber (и значит fibermap) построен на fasthttp, исходящий остаётся на `net/http`:

1. **Interop:** AWS SDK, Stripe, каждая OAuth/JWKS библиотека принимают `*http.Client`. Возврат `*fasthttp.Client` заставил бы выбирать между нашим retry-слоем и каждым SDK.
2. **Экосистема RoundTripper:** otel HTTP-инструментация, Prometheus middleware, auth round-tripper'ы — всё `http.RoundTripper`. У fasthttp нет эквивалента.
3. **Use case:** fasthttp оптимизирует high-throughput inbound. Client-side throughput для типичного microservice-исходящего редко bottleneck.

Server-side fasthttp (fiber) — это асимметрия, и это нормально — другие задачи.

## Тестирование

Тестируйте против `httptest.NewServer`:

```go
func TestRetryOn503(t *testing.T) {
    var n atomic.Int32
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if n.Add(1) < 3 {
            w.WriteHeader(503)
            return
        }
        w.WriteHeader(200)
    }))
    t.Cleanup(srv.Close)

    c, _ := httpc.New(httpc.Config{
        Timeout: time.Second, MaxRetries: 3,
        BackoffBase: time.Millisecond, BackoffMax: 10 * time.Millisecond,
    })
    resp, err := c.Get(srv.URL)
    if err != nil || resp.StatusCode != 200 { t.Fatal(err, resp.StatusCode) }
    if n.Load() != 3 { t.Errorf("got %d, want 3", n.Load()) }
}
```

Используйте маленькие `BackoffBase`/`BackoffMax` в тестах, чтобы они оставались быстрыми.

## Ограничения

- **Retry-политика hard-coded.** Только идемпотентные, фиксированный набор статусов. Никакого `WithRetryClassifier` пока нет — приедет как additive-feature, если понадобится.
- **Нет JSON-хелперов.** Декодируйте в своём handler'е (`json.NewDecoder(resp.Body).Decode(&out)`). Пакет остаётся transport'ом.
- **Per-host concurrency cap'ы живут на `http.Transport`.** Конфигурируйте через `WithBaseTransport(custom)`, если нужны.
- **Body-buffering для streaming-body без `GetBody` — задача caller'а.** httpc не будет молча потреблять + буферить произвольные upload-стримы.

## См. также

- [`clients/apimap`](../apimap/README.md) — декларативный outbound-слой, построенный поверх httpc
- [`errs`](../../errs/README.md) — error-контракт для валидационных failures
- [`examples/urlshort`](../../examples/urlshort/README.md) — использует httpc для произвольного URL-fetching'а в пакете enrich
</content>
