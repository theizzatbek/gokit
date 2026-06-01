# clients/natsmap

Декларативный YAML-слой над `clients/nats` для NATS / JetStream подписчиков и публишеров. Подписчики и публишеры описываются в YAML; Go-код регистрирует типизированные хендлеры и публишеры по имени; `Build` возвращает goroutine-safe `*Runtime`, выставляющий `Drain()` и `Publish[T](...)`. Симметрично `clients/apimap` для исходящего HTTP.

**Импорт:** `github.com/theizzatbek/gokit/clients/natsmap`
**Зависит от:** `gopkg.in/yaml.v3` + `github.com/theizzatbek/gokit/errs` + `github.com/theizzatbek/gokit/clients/nats`

## Зачем это нужно

`clients/nats` даёт вам типизированные `Publisher[T]` и `Subscribe[T]` с auto-Ack семантикой. Это НЕ решает **subject-каталог** — каждый сервис всё ещё руками пишет тот же Subscribe-вызов на каждый subject, с тем же boilerplate вокруг durable-имён, MaxInFlight, MaxDeliver, backoff-кривых и start-from политик.

`natsmap` — недостающий слой: подписчики и публишеры живут в YAML; код регистрирует типизированные хендлеры и публикует по имени. Один grep по `*.yaml` отвечает "какие subjects этот сервис потребляет? какие subjects он публикует?". Симметрично `clients/apimap` для исходящего HTTP — более широкая тема кита: декларативная внешняя shell, оборачивающая типизированный Go-core.

## Quickstart

`events.yaml`:

```yaml
subscribers:
  - name: invoice_sender
    subject: orders.created
    durable: invoice-sender
    max_in_flight: 16
    max_deliver: 5
    ack_wait: 30s
publishers:
  - name: orders.created
    subject: orders.created
    headers:
      X-Source: orders-svc
```

`main.go`:

```go
package main

import (
    "context"
    "log/slog"
    "time"

    natsclient "github.com/theizzatbek/gokit/clients/nats"
    "github.com/theizzatbek/gokit/clients/natsmap"
)

type OrderCreated struct {
    ID    string `json:"id"`
    Total int64  `json:"total"`
}

func main() {
    ctx := context.Background()
    logger := slog.Default()

    c, err := natsclient.Connect(ctx, natsclient.Config{
        URL:  "nats://localhost:4222",
        Name: "orders-svc",
    }, natsclient.WithLogger(logger))
    if err != nil {
        panic(err)
    }
    defer c.Close()

    // Идемпотентно — безопасно на каждом старте.
    _ = c.EnsureStream(ctx, natsclient.StreamConfig{
        Name:     "ORDERS",
        Subjects: []string{"orders.>"},
        MaxAge:   7 * 24 * time.Hour,
        Storage:  natsclient.StorageFile,
    })

    eng := natsmap.New()
    if err := eng.LoadFile("events.yaml"); err != nil {
        panic(err)
    }
    natsmap.RegisterHandler[OrderCreated](eng, "invoice_sender",
        func(ctx context.Context, m natsclient.Msg[OrderCreated]) error {
            logger.Info("invoice", "id", m.Data.ID, "total", m.Data.Total)
            return nil // → Ack
        })
    natsmap.RegisterPublisher[OrderCreated](eng, "orders.created")

    rt, err := eng.Build(ctx, c, natsmap.WithLogger(logger))
    if err != nil {
        panic(err)
    }
    defer rt.Drain()

    _ = natsmap.Publish[OrderCreated](ctx, rt, "orders.created",
        OrderCreated{ID: "o1", Total: 4200})
}
```

## YAML-схемы

### `subscribers.yaml`

```yaml
subscribers:
  - name: <string>                 # обязательно, уникально в engine; пара с RegisterHandler[T]
    subject: <string>              # обязательно; NATS-subject (literal или wildcard)
    durable: <string>              # опционально; имя durable-consumer'а (переживает restart)
    max_in_flight: <int>           # опционально; семафор concurrency handler'ов (>= 0)
    max_deliver: <int>             # опционально; total попыток delivery до Term (>= 0)
    ack_wait: <duration>           # опционально; redeliver, если Ack не виден внутри этого окна
    queue_group: <string>          # опционально; round-robin между queue-членами (load balancing)
    backoff:                       # опционально; per-redelivery backoff
      type: exponential|fixed      # по умолчанию "exponential"
      base: <duration>             # обязательно, когда backoff: установлен; должен быть > 0
      max: <duration>              # опционально; по умолчанию base*32 для exponential, игнорируется для fixed
    start_from: <policy>           # опционально; см. "start_from формы" ниже
    filter_subject: <string>       # опционально; override subject-фильтра на JetStream consumer'е
    batch_size: <int>              # опционально; > 0 переключает в batched mode (см. ниже)
    batch_interval: <duration>     # опционально; max wait между батчами; по умолчанию 1s, когда batch_size > 0
```

#### Batched-подписчики

Установите `batch_size: N`, чтобы opt'нуть subscriber в JetStream Pull mode:
кит fetch'ит до N сообщений с deadline'ом `batch_interval` (по умолчанию 1s)
и отдаёт их batched-handler'у, зарегистрированному через
`natsmap.RegisterBatchedHandler[T]`:

```go
natsmap.RegisterBatchedHandler[OrderCreated](e, "invoice_sender",
    func(ctx context.Context, batch []natsclient.Msg[OrderCreated]) error {
        return persistAll(ctx, batch) // одна транзакция
    })
```

Return handler'а драйвит **all-or-nothing** ack-семантику:

- `return nil` → кит Ack'ает каждое сообщение в батче (Postgres-style
  `COMMIT`-аналог).
- `return err` → кит Nak'ает каждое сообщение → JetStream re-deliver'ит
  весь батч на следующем fetch'е (`ROLLBACK`).

Mode-mismatch'и ловятся на `Build`:

| Code | Cause |
|---|---|
| `natsmap_batch_handler_required` | YAML имеет `batch_size > 0`, но был вызван `RegisterHandler[T]`. |
| `natsmap_regular_handler_required` | YAML без `batch_size`, но был вызван `RegisterBatchedHandler[T]`. |

Реализация живёт в `clients/natsmap/batched.go`: per-subscriber goroutine
loop'ает `nats.Subscription.Fetch(batch_size, MaxWait=batch_interval)`,
декодирует payloads через зарегистрированный codec, зовёт типизированный
batched-handler, потом обходит message-срез ack'ая или nak'ая в зависимости
от return'а.

Decode-failures Term'ают offending-сообщение (poison-pill suppression)
и удаляют из живого батча; оставшиеся успешно-декодированные сообщения
всё равно проходят через handler.

### `publishers.yaml`

```yaml
publishers:
  - name: <string>                 # обязательно, уникально в engine; пара с RegisterPublisher[T]
    subject: <string>              # обязательно; NATS-subject, в который publisher таргетит
    headers:                       # опционально; map[string]string, применяется к каждой публикации
      <Header-Name>: <value>       # расширяется до []string{value} при отправке
```

### Комбинированный `events.yaml`

Оба блока могут жить в одном файле:

```yaml
subscribers:
  - name: invoice_sender
    subject: orders.created
    durable: invoice-sender
publishers:
  - name: orders.created
    subject: orders.created
```

`LoadFile` additive — вызов его несколько раз append'ит записи в один engine. Вы можете держать `subscribers.yaml` и `publishers.yaml` отдельными файлами, или мерджить их в один `events.yaml`. Обе формы first-class.

### Env-var substitution

`${VAR_NAME}` где угодно в YAML резолвится против `os.Getenv` на LoadFile-time (regex `[A-Z_][A-Z0-9_]*` — только uppercase). Полезно для environment-specific subject-префиксов:

```yaml
subscribers:
  - name: invoice_sender
    subject: ${ENV}.orders.created
    durable: invoice-sender-${ENV}
```

| Code | Когда |
|---|---|
| `natsmap_env_var_unset` | `${FOO}` упомянут, но `FOO` не в env'е |
| `natsmap_env_var_malformed` | Форма `${...}` не соответствует regex (например, `${lower-case}`) |

### Явные env-значения через `WithEnv`

Если ваш сервис уже имеет typed-config, передайте значения явно:

```go
e := natsmap.New(natsmap.WithEnv(map[string]string{
    "ORDERS_STREAM_PREFIX": cfg.OrdersStreamPrefix,
}))
e.LoadFile("subscribers.yaml")
```

Map consult'ируется первой; на miss fallback'ится на `os.LookupEnv`. Оба miss → `natsmap_env_var_unset`.

## Декларация streams

natsmap может ensure'ить JetStream-стримы вместе с подписчиками и
публишерами, которые их используют. Две формы:

### Explicit-список

```yaml
streams:
  - name: ORDERS
    subjects: [orders.>]
    storage: file              # file (по умолчанию) | memory
    retention: limits          # limits (по умолчанию) | interest | work_queue
    max_age: 168h
    max_bytes: 0               # bytes; 0 = unlimited
    max_msgs: 0
    replicas: 1
    dedup: 2m
```

### Auto-derive из subjects

```yaml
streams: auto

publishers:
  - { name: orders_out, subject: orders.created }
```

`streams: auto` обходит subscriber + publisher subjects, группирует по первому
сегменту (`orders.created` → `orders`) и создаёт один stream per группу с
wildcard-subjects (`orders.>`) и безопасными defaults (file-storage,
limits-retention, unlimited-age).

Для production предпочитайте explicit-список — defaults не tuned'ы
(без MaxAge, без Replicas > 1). `auto` отлично для dev'а и примеров.

Комбинирование `auto` и explicit-списка в одном engine →
`natsmap_streams_auto_conflict` ошибка на Build.

## Multi-node поведение

Когда тот же сервис работает на N инстансах, subscriber-defaults должны
избегать борьбы двух инстансов за тот же durable-consumer. natsmap
применяет эти правила:

| YAML `durable` | YAML `queue_group` | Effective `durable` | Effective `queue_group` |
|---|---|---|---|
| `""` (пропущено) | `""` (пропущено) | `name` | `name` (+ ServerGroup-суффикс) |
| `""` | `"workers"` | `name` | `workers` (explicit побеждает) |
| `"foo"` | `""` | `foo` | `""` (user контролирует durable) |
| `"foo"` | `"bar"` | `foo` | `bar` |
| `"ephemeral"` | `""` | `""` (true ephemeral) | `""` |
| `"ephemeral"` | `"bar"` | `""` | `bar` |

**По умолчанию = load-balanced**: subscriber без явного config'а получает
durable-consumer, привязанный к queue-group, оба именованы по имени
subscriber'а. N инстансов → N consumer'ов в одной queue-group → каждое
сообщение идёт ровно одному.

**Broadcast = explicit ephemeral**: `durable: ephemeral` opts каждый
инстанс в свой ephemeral-consumer. Каждый инстанс видит каждое сообщение.

### Паттерн ServerGroup (cross-region)

Установите `service.WithServerGroup("dc1")` (или env `SERVICE_SERVER_GROUP=dc1`), чтобы суффиксить auto-derived queue-groups:

```
subscriber name = invoice_sender
ServerGroup     = dc1
queue group     = invoice_sender-dc1
```

Инстансы в DC1 формируют queue-group `invoice_sender-dc1`; инстансы DC2
формируют `invoice_sender-dc2`. Каждый регион обрабатывает события
независимо.

### Формы `start_from`

| Значение | Смысл |
|---|---|
| `new` (по умолчанию) | Доставлять только сообщения, опубликованные после создания consumer'а |
| `all` | Replay'ить каждое сообщение в stream'е с начала |
| `from_seq:<int>` | Стартовать с заданной JetStream sequence-number |
| `from_time:<RFC3339>` | Стартовать с первого сообщения на заданном времени или после (например, `from_time:2026-01-15T00:00:00Z`) |

### Knob'ы `backoff`

| Поле | Обязательно | Заметки |
|---|---|---|
| `type` | да | `exponential` (по умолчанию) или `fixed` |
| `base` | да | Initial-delay; для `fixed` это единственный delay |
| `max` | нет | Upper-cap для `exponential`; по умолчанию `base * 32`; игнорируется для `fixed` |

## Публичный API

```go
type Engine struct{ /* unexported */ }
type Runtime struct{ /* unexported */ }
type Option func(*options)

// Engine lifecycle (build-once)
func New() *Engine
func (e *Engine) LoadFile(path string) error              // additive — зовите несколько раз
func (e *Engine) LoadBytes(b []byte) error                // additive — зовите несколько раз

// Типизированная регистрация — panic на дублирующем имени или post-Build вызове
func RegisterHandler[T any](e *Engine, name string,
    h func(ctx context.Context, m natsclient.Msg[T]) error)
func RegisterPublisher[T any](e *Engine, name string)

// Build: валидирует всё, открывает subscriptions, возвращает *Runtime.
// Несколько validation-failures агрегируются через errors.Join.
func (e *Engine) Build(ctx context.Context, c *natsclient.Client, opts ...Option) (*Runtime, error)

// Опции
func WithLogger(*slog.Logger) Option
func WithMetrics(prometheus.Registerer) Option

// Runtime — goroutine-safe
func Publish[T any](ctx context.Context, r *Runtime, name string, payload T) error
func PublishWithHeaders[T any](ctx context.Context, r *Runtime, name string,
    payload T, headers map[string][]string) error
func (r *Runtime) Drain() error                            // идемпотентен
func (r *Runtime) SubscriberNames() []string               // отсортирован
func (r *Runtime) PublisherNames() []string                // отсортирован
```

`PublishWithHeaders` мерджит per-call headers поверх YAML-declared статических headers; per-call записи побеждают на коллизии.

## Common patterns

### Через `gokit/service`

Установите `NATSMAP_SUBSCRIBERS_PATH` / `NATSMAP_PUBLISHERS_PATH` в окружении (один или оба — это opt-in trigger). Проведите типизированные регистрации с `service.WithNATSMapRegistration`:

```go
svc, err := service.New[ReqCtx, MyClaims](ctx, cfg,
    service.WithNATSMapRegistration(func(e *natsmap.Engine) {
        natsmap.RegisterHandler[OrderCreated](e, "invoice_sender", handleInvoice)
        natsmap.RegisterPublisher[OrderCreated](e, "orders.created")
    }),
)
// svc.NATSMap — это *natsmap.Runtime; svc.Run() дренит его на shutdown'е
// до того, как tears down лежащее снизу NATS-соединение.
_ = svc.Run()
```

### Standalone проводка

`Connect → EnsureStream → New → LoadFile → Register* → Build` (см. Quickstart выше). Никакой service-фреймворк не требуется.

### Queue groups для load balancing

```yaml
subscribers:
  - name: invoice_sender
    subject: orders.created
    queue_group: invoice-workers
    durable: invoice-workers
```

Несколько инстансов того же сервиса с `queue_group: invoice-workers` шарят нагрузку: каждое сообщение доставляется ровно одному queue-члену, round-robin. Пара `queue_group` с shared `durable`-именем делает consumer'а переживающим restart'ы.

### Mixed YAML-файлы

```go
_ = eng.LoadFile("subscribers.yaml")
_ = eng.LoadFile("publishers.yaml")
// — или —
_ = eng.LoadFile("events.yaml") // комбинированный
```

`LoadFile` аккумулирует в тот же engine. Используйте layout, который подходит вашему репо.

### Type-mismatch — startup vs runtime

| Failure | Когда detected | Механизм |
|---|---|---|
| Дубликат `RegisterHandler[T]` на одно имя | startup (регистрация) | panic с `natsmap_duplicate_subscriber` |
| `Register*` после `Build` | startup (регистрация) | panic с `natsmap_already_built` |
| YAML-subscriber без `RegisterHandler` | startup (Build) | ошибка `natsmap_handler_not_registered` |
| `RegisterHandler` для unknown YAML-имени | startup (Build) | ошибка `natsmap_handler_unknown` |
| `Publish[WrongType]` на runtime | runtime | ошибка `natsmap_publisher_type_mismatch` |

Интент: каждый YAML-vs-code mismatch всплывает на Build, до того, как открывается subscription. Wrong-type публикации всё ещё всплывают на call-site, так что test-coverage их ловит.

## Error-модель

Все ошибки — `*errs.Error` со стабильным `Code`.

### Build-time (собранный через `errors.Join`)

| Code | Kind | Когда |
|---|---|---|
| `natsmap_read_file` | Validation | `LoadFile` не может прочитать файл |
| `natsmap_parse_yaml` | Validation | YAML decode-failure |
| `natsmap_env_var_unset` | Validation | `${VAR}` ссылается на unset env-var |
| `natsmap_env_var_malformed` | Validation | `${...}` не соответствует `[A-Z_][A-Z0-9_]*` |
| `natsmap_no_entries` | Validation | YAML распарсен, но нет subscribers и нет publishers |
| `natsmap_missing_name` | Validation | subscriber/publisher-запись без `name` |
| `natsmap_missing_subject` | Validation | subscriber/publisher-запись без `subject` |
| `natsmap_duplicate_subscriber` | Validation | два subscribers шарят `name` |
| `natsmap_duplicate_publisher` | Validation | два publishers шарят `name` |
| `natsmap_invalid_max_in_flight` | Validation | `max_in_flight < 0` |
| `natsmap_invalid_max_deliver` | Validation | `max_deliver < 0` |
| `natsmap_invalid_ack_wait` | Validation | `ack_wait < 0` |
| `natsmap_invalid_backoff` | Validation | `backoff.type` unknown, `base <= 0` или `max < base` |
| `natsmap_invalid_start_from` | Validation | `start_from` вне `new|all|from_seq:<int>|from_time:<RFC3339>` |
| `natsmap_handler_not_registered` | Validation | YAML-subscriber без matching `RegisterHandler[T]` |
| `natsmap_handler_unknown` | Validation | `RegisterHandler` для имени, отсутствующего в YAML |
| `natsmap_publisher_not_registered` | Validation | YAML-publisher без matching `RegisterPublisher[T]` |
| `natsmap_publisher_unknown` | Validation | `RegisterPublisher` для имени, отсутствующего в YAML |
| `natsmap_subscribe_failed` | Unavailable | лежащий снизу `natsclient.SubscribeRaw` зафейлился |
| `natsmap_already_built` | Validation | `Build` вызван дважды, или `Register*` после `Build` |

### Runtime (из `Publish` / `PublishWithHeaders`)

| Code | Kind | Когда |
|---|---|---|
| `natsmap_unknown_publisher` | NotFound | `name` не в YAML / не зарегистрирован |
| `natsmap_publisher_type_mismatch` | Validation | `Publish[T]` `T` отличается от зарегистрированного типа |
| `natsmap_publish_failed` | Unavailable | лежащая снизу `natsclient` публикация вернула ошибку |

## Observability

### `WithLogger`

`WithLogger(*slog.Logger)` устанавливает логгер, который natsmap использует для natsmap-level событий (в настоящее время registration-warning'и; будущий hot-reload). Per-subscription handler-логи — decode-failures (→ Term), handler-ошибки (→ Nak с backoff), max-deliver exceeded — принадлежат `clients/nats`. Сконфигурируйте и тот, передав тот же логгер в `natsclient.Connect(..., natsclient.WithLogger(logger))`.

### `WithMetrics`

`WithMetrics(prometheus.Registerer)` принимается для симметрии с `apimap`. Сам natsmap в настоящее время не экспонирует collectors; subscription/publish-метрики (in-flight gauge, handler success/error counter, decode-error counter, publish-duration histogram) приходят из `clients/nats.WithMetrics`.

```go
c, _ := natsclient.Connect(ctx, cfg,
    natsclient.WithLogger(logger),
    natsclient.WithMetrics(promReg),
)
rt, _ := eng.Build(ctx, c,
    natsmap.WithLogger(logger),   // для будущих natsmap-level событий
    natsmap.WithMetrics(promReg), // зарезервировано
)
```

## Тестирование

Unit-тесты работают без Docker:

```bash
go test -short ./clients/natsmap/
```

Integration smoke (`TestRuntime_PublishAndReceive`, `TestRuntime_BuildAggregatesValidationErrors`, `TestRuntime_BuildTwiceFails`) поднимает `nats:2-alpine` с `-js` через `testcontainers-go/modules/nats` — Docker требуется:

```bash
go test ./clients/natsmap/
```

Для своих тестов следуйте тому же паттерну: поднимайте testcontainer, `EnsureStream`, `LoadBytes` inline-YAML, `Register*`, `Build`, `defer rt.Drain()`.

## Ограничения

- **Нет hot-reload YAML.** Грузится один раз на старте. Будущий `WithHotReload()` планируется.
- **`Msg[T].Raw()` возвращает nil для natsmap-routed сообщений.** Reflection-bridge декодирует payloads в freshly-allocated `*T` и никогда не сохраняет лежащий снизу `*nats.Msg`. Если нужен raw-доступ (headers-manipulation, manual Ack-timing, JetStream-метаданные за пределами того, что выставляет `Msg[T]`), используйте `natsclient.Subscribe[T]` напрямую.
- **Один codec на `*natsclient.Client`.** Унаследовано от `clients/nats`. Heterogeneous wire-format'ы по topic'ам требуют нескольких клиентов.
- **Нет "web-publisher" пока.** Будущий пакет смост'ит `fibermap`-route с NATS-публикацией в YAML; вне scope'а здесь.
- **`Build` открывает каждую subscription синхронно.** Длинный subscriber-список с медленным JetStream создаёт медленный startup; failures агрегируются через `errors.Join`.
- **Нет subject-имени валидации против stream'а.** Если `subject:` не матчит stream, сконфигурированный на server'е, лежащий снизу `Subscribe` фейлится на Build с `natsmap_subscribe_failed`.

## См. также

- [`clients/nats`](../nats/README.md) — типизированная JetStream-обёртка, лежащая под natsmap'ом (Publisher[T], Subscribe[T], EnsureStream)
- [`clients/apimap`](../apimap/README.md) — симметричный декларативный HTTP-слой (inbound/outbound аналог)
- [`service`](../../service/README.md) — авто-подключает natsmap, когда `NATSMAP_SUBSCRIBERS_PATH` / `NATSMAP_PUBLISHERS_PATH` установлены
- [`errs`](../../errs/README.md) — error-контракт
- [`examples/urlshort`](../../examples/urlshort/README.md) — использует natsmap для `urlshort.link.{created,visited}` publisher'ов + subscriber'ов
</content>
