# clients/nats

Типизированная обёртка над NATS / JetStream клиентом (пакет `natsclient`). `Connect(ctx, cfg, opts...) (*Client, error)` открывает соединение + JetStream context. Generic `Publisher[T]` / `Subscribe[T]` поверх опциональной `Codec` (JSON по умолчанию). Auto-ack handler model: handler возвращает nil → Ack, err → Nak с экспоненциальным backoff'ом, decode-fail → Term (poison pill). `MaxInFlight` семафор cap'ит handler-concurrency. Идемпотентный `EnsureStream` для app-managed stream-lifecycle'а.

**Импорт:** `github.com/theizzatbek/gokit/clients/nats` (пакет `natsclient` — именование избегает коллизии с `nats-io/nats.go`)
**Зависит от:** `nats-io/nats.go` + `prometheus/client_golang` + `github.com/theizzatbek/gokit/errs`

## Зачем это нужно

Raw `nats.go` API — `[]byte`-based и нетипизированный. Каждый сервис в итоге пишет один и тот же publish-encode-with-codec и subscribe-decode-Ack-Nak boilerplate, с тонкими багами вокруг "что если decode фейлится навсегда — мы бесконечно re-deliver'им?". `natsclient` — это такой бандл: типизированный `Publisher[T]` + `Subscribe[T]` с auto-Ack, opinionated handler model (decode fail → Term, не redeliver), MaxInFlight backpressure и стабильным error-mapping'ом в `*errs.Error`.

## Quickstart

```go
import (
    "context"
    "time"
    natsclient "github.com/theizzatbek/gokit/clients/nats"
)

type OrderCreated struct {
    ID     string `json:"id"`
    Amount int64  `json:"amount"`
}

func main() {
    c, err := natsclient.Connect(ctx, natsclient.Config{
        URL:  "nats://localhost:4222",
        Name: "orders-svc",
    }, natsclient.WithLogger(logger), natsclient.WithMetrics(promReg))
    if err != nil { return err }
    defer c.Close()

    // Идемпотентно — безопасно на каждом старте.
    err = c.EnsureStream(ctx, natsclient.StreamConfig{
        Name:     "ORDERS",
        Subjects: []string{"orders.>"},
        MaxAge:   7 * 24 * time.Hour,
        Storage:  natsclient.StorageFile,
    })

    // Producer
    pub := natsclient.NewPublisher[OrderCreated](c)
    pub.Publish(ctx, "orders.created", OrderCreated{ID: "abc", Amount: 100})

    // Consumer
    sub, err := natsclient.Subscribe[OrderCreated](ctx, c, "orders.created",
        func(ctx context.Context, m natsclient.Msg[OrderCreated]) error {
            if err := sendInvoice(ctx, m.Data); err != nil {
                return err  // → Nak с экспоненциальным backoff'ом
            }
            return nil      // → Ack
        },
        natsclient.WithDurable("invoice-sender"),
        natsclient.WithMaxInFlight(16),
        natsclient.WithMaxDeliver(5),
    )
    defer sub.Drain()
}
```

## Конфигурация

### `natsclient.Config`

| Поле | По умолчанию | Заметки |
|---|---|---|
| `URL` | — (обязательно) | `nats://host:port`, comma-separated для кластера |
| `Name` | filepath.Base(os.Args[0]) | Имя клиента, видимое в NATS-мониторинге |
| `Timeout` | 5s | Connect timeout |
| `Token` | "" | Token-auth (выберите максимум ОДИН метод auth) |
| `User`, `Password` | "" | Basic-auth (оба требуются вместе) |
| `CredsFile` | "" | Путь к NATS 2.0+ JWT creds-файлу |
| `NKeySeed` | "" | Сырой NKey-seed |
| `MaxReconnects` | -1 (infinite) | Установите положительное, чтобы сдаться |
| `ReconnectWait` | 2s | Задержка между попытками reconnect'а |
| `ConnectMaxRetries` | `0` (no retry) | K8s boot-resilience |
| `ConnectBackoffBase` | `0` | K8s boot-resilience |
| `ConnectBackoffMax` | `0` | K8s boot-resilience |

### Connect retry (K8s boot-resilience)

Три опциональных Config-поля cap'ят initial-Connect retry-поведение:

| Поле | Env (через `gokit/service`) | По умолчанию |
|---|---|---|
| `ConnectMaxRetries` | `NATS_CONNECT_MAX_RETRIES` | `0` (no retry) |
| `ConnectBackoffBase` | `NATS_CONNECT_BACKOFF_BASE` | `0` |
| `ConnectBackoffMax` | `NATS_CONNECT_BACKOFF_MAX` | `0` |

Дефолт кита — fail-fast (1 попытка). При использовании `gokit/service`,
service авто-инжектит 5 retries с 1s base / 16s cap (~31s
total). Чтобы отключить, установите `NATS_CONNECT_MAX_RETRIES=-1` или вызовите `service.WithoutConnectRetry()`.

Это retry только initial-Connect'а. Post-connection drops обрабатываются существующими `MaxReconnects` + `ReconnectWait` у `nats.go` (не меняются этой фичей).

### Опции

| Опция | По умолчанию | Заметки |
|---|---|---|
| `WithCodec(Codec)` | `JSONCodec` | Wire-format для ВСЕХ публишеров и подписчиков |
| `WithLogger(*slog.Logger)` | silent | Reconnect/disconnect события, handler-ошибки, decode failures |
| `WithMetrics(prometheus.Registerer)` | нет коллекторов | publish/decode/handler counters + histograms |
| `WithReconnectHandler(fn)` | none | Срабатывает после каждого успешного reconnect'а |
| `WithDisconnectErrHandler(fn)` | none | Срабатывает на каждом disconnect'е |
| `WithClosedHandler(fn)` | none | Срабатывает, когда соединение окончательно закрыто |

## Common patterns

### Stream lifecycle — `EnsureStream`

`EnsureStream` идемпотентен: создаёт stream, если отсутствует, валидирует config, если присутствует, возвращает существующий stream в противном случае. Безопасно зватть на каждом старте.

```go
err := c.EnsureStream(ctx, natsclient.StreamConfig{
    Name:      "ORDERS",
    Subjects:  []string{"orders.>"},
    Retention: natsclient.RetentionLimits,  // Limits | Interest | WorkQueue
    Storage:   natsclient.StorageFile,      // File | Memory
    MaxAge:    7 * 24 * time.Hour,
    MaxBytes:  10 * 1024 * 1024 * 1024,    // 10 GiB
    MaxMsgs:   1_000_000,
    Replicas:  3,
    Dedup:     2 * time.Minute,             // server-side Nats-Msg-Id dedup window
})
```

Если stream с тем же именем существует с другим config'ом, `EnsureStream` возвращает `*errs.Error{Code: "stream_config_invalid"}` — explicit failure, так что вы молча не работаете на неверном config'е.

### Публикация

```go
pub := natsclient.NewPublisher[OrderCreated](c)
if err := pub.Publish(ctx, "orders.created", evt); err != nil {
    // *errs.Error{Code: "publish_failed"} при JetStream rejection,
    // *errs.Error{Code: "encode_failed"} при codec-ошибке
}

// С Nats-Msg-Id для dedup'а
err := pub.PublishWithHeaders(ctx, "orders.created", evt, map[string][]string{
    "Nats-Msg-Id": {evt.ID},
})
```

Публикации идут через JetStream (subjects, совпадающие со stream'ом) или core NATS (другие) автоматически — `Publisher` introspect'ит connected-stream config.

### Подписка — auto-ack handler model

```go
sub, err := natsclient.Subscribe[OrderCreated](ctx, c, "orders.created",
    func(ctx context.Context, m natsclient.Msg[OrderCreated]) error {
        return processOrder(ctx, m.Data)  // nil → Ack, err → Nak
    },
    natsclient.WithDurable("invoice-sender"),
    natsclient.WithMaxInFlight(16),
    natsclient.WithMaxDeliver(5),
    natsclient.WithAckWait(30*time.Second),
    natsclient.WithBackoff(func(redeliveries int) time.Duration {
        // экспонента 1s, 5s, 25s, …
        d := time.Duration(1<<redeliveries) * time.Second
        if d > time.Minute { return time.Minute }
        return d
    }),
)
defer sub.Drain()  // graceful: stop pulling, finish in-flight, ack remaining
```

| Handler возвращает | Действие |
|---|---|
| `nil` | Ack |
| non-nil `error` | Nak (с backoff'ом, если сконфигурирован) |
| decode failure (до запуска handler'а) | Term — poison pill, постоянно удалён из stream'а |
| panic | Recover'ится, обрабатывается как error → Nak |

После исчерпания `WithMaxDeliver`, message Term'ается и логируется на ERROR.

### Subscribe-опции

| Опция | По умолчанию | Заметки |
|---|---|---|
| `WithDurable(name)` | пусто (ephemeral) | Имя JetStream-durable consumer'а — переживает restart подписчика |
| `WithMaxInFlight(n)` | 1 | Семафор concurrent handler'ов (backpressure) |
| `WithAckWait(d)` | 30s | NATS redeliver'ит, если Ack не виден внутри `d` |
| `WithMaxDeliver(n)` | 5 | Total попыток delivery до Term'а |
| `WithBackoff(fn)` | экспонента | `fn(redeliveries) time.Duration` |
| `WithStartFrom(StartPolicy)` | StartNew | Где consumer стартует: `StartNew` / `StartAll` / `StartFromTime(t)` / `StartFromSequence(seq)` |
| `WithFilterSubject(s)` | subject из вызова | Override фильтра subject'а для consumer'а |
| `WithQueueGroup(g)` | none | Distributed work-queue семантика (round-robin между queue-членами) |
| `WithErrorClassifier(fn)` | `nil→Ack / ErrPoison→Term / иначе→Nak` | Декларативное routing handler-ошибок в Ack/Nak/Term |
| `WithAckProgress(d)` | off | Auto-heartbeat `InProgress()` каждые `d` пока handler работает (для long-running) |
| `WithPanicHandler(fn)` | nil | Callback на recovered panic'е (Sentry capture etc); kit и так делает Warn-log + Nak |

### Handler resilience — classifier / panic / ack-progress

```go
sub, _ := natsclient.Subscribe[OrderCreated](ctx, c, "orders.created", handler,
    natsclient.WithDurable("invoice-sender"),
    natsclient.WithAckWait(30*time.Second),

    // Long handler > AckWait? Авто-heartbeat сохраняет lease.
    natsclient.WithAckProgress(10*time.Second),

    // Validation-ошибки = Term (не redeliver'им), transient = Nak.
    natsclient.WithErrorClassifier(func(err error) natsclient.AckAction {
        var e *errs.Error
        if errors.As(err, &e) && e.Kind == errs.KindValidation {
            return natsclient.AckActTerm
        }
        return natsclient.AckActNak
    }),

    // Panic в handler'е recover'ится → Nak; этот callback для Sentry/audit.
    natsclient.WithPanicHandler(func(p any) { sentry.CurrentHub().Recover(p) }),
)
```

Также есть `m.InProgress()` (на `Msg[T]` и `RawMsg`) — ручной heartbeat, когда `WithAckProgress` слишком грубый.

### Pull-mode consumer

Push (по умолчанию) = NATS пушит handler'у когда удобно. **Pull** = caller fetch'ит batches когда удобно. Лучше для cron/batch-job'ов и backpressure-sensitive workers.

```go
ps, _ := natsclient.NewPullSubscription[OrderCreated](c, "orders.created",
    natsclient.WithDurable("nightly-reporter"),   // обязательно для pull
    natsclient.WithMaxDeliver(3),
)
defer ps.Drain()

// Вариант 1: explicit Fetch + Ack
batch, _ := ps.Fetch(ctx, 100, 5*time.Second)
for _, m := range batch {
    if err := process(m.Data); err != nil { _ = m.Nak(); continue }
    _ = m.Ack()
}

// Вариант 2: Run blocking loop
_ = ps.Run(ctx, func(ctx context.Context, m natsclient.Msg[OrderCreated]) error {
    return process(m.Data)
}, 100, 5*time.Second)
```

Decode-failures в `Fetch` авто-Term'ются (poison-pill suppression) и логируются на ERROR; остальные decoded успешно проходят в slice. Pull требует `WithDurable`.

### Request/Reply (typed RPC)

```go
// Server-side: subscribe + auto-respond
sub, _ := natsclient.Reply[GetUser, User](ctx, c, "users.get", "users-svc",
    func(ctx context.Context, req GetUser) (User, error) {
        return usersRepo.ByID(ctx, req.ID)
    })
defer sub.Drain()

// Client-side: typed request with timeout
user, err := natsclient.Request[GetUser, User](ctx, c, "users.get",
    GetUser{ID: "42"}, 2*time.Second)
// *errs.Error{Code: "request_timeout"} при отсутствии reply
```

`queueGroup` (empty = без LB) распределяет один request на одного из N members группы.

### KV bucket (типизированный)

```go
_, err := c.EnsureKVBucket(ctx, natsclient.KVConfig{
    Bucket:  "feature-flags",
    History: 5,
    TTL:     24 * time.Hour,
})

kv, _ := natsclient.NewKV[FeatureFlag](c, "feature-flags")
rev, _ := kv.Put(ctx, "checkout.new-flow", FeatureFlag{Enabled: true})
flag, gotRev, _ := kv.Get(ctx, "checkout.new-flow")           // *errs.Error{KindNotFound} на miss
_, err = kv.Update(ctx, "checkout.new-flow", newVal, gotRev)   // CAS — Conflict на stale rev
_ = kv.Delete(ctx, "checkout.new-flow")
kv.Raw() // *nats.KeyValue для Watch / History / ListKeys
```

### TLS

```go
c, _ := natsclient.Connect(ctx, cfg,
    natsclient.WithRootCAs(pool),                            // self-signed / private CA
    natsclient.WithClientCert("client.pem", "client.key"),   // mTLS
)
// или один полный *tls.Config:
c, _ := natsclient.Connect(ctx, cfg, natsclient.WithTLSConfig(myTLS))
```

`WithTLSConfig` и `WithRootCAs`/`WithClientCert` взаимоисключающие — используйте либо verbatim, либо piecewise.

### Кастомный codec (например, protobuf)

```go
type ProtoCodec struct{}
func (ProtoCodec) Encode(v any) ([]byte, error) { return proto.Marshal(v.(proto.Message)) }
func (ProtoCodec) Decode(data []byte, v any) error { return proto.Unmarshal(data, v.(proto.Message)) }

c, _ := natsclient.Connect(ctx, cfg, natsclient.WithCodec(ProtoCodec{}))
```

Один codec на Client — держит wire-format консистентным service-wide.

## Error-модель

Все ошибки — `*errs.Error` со стабильным `Code`:

| Code | Kind | Когда |
|---|---|---|
| `connect_failed` | Unavailable | Initial connect (DNS, refused, auth-fail) |
| `jetstream_unavailable` | Unavailable | JetStream context unreachable |
| `missing_url` / `auth_ambiguous` | Validation | Config-ошибки на Connect |
| `invalid_nkey` | Validation | NKeySeed unparseable |
| `stream_not_found` / `stream_op_failed` / `stream_config_invalid` | Различные | EnsureStream + stream-операции |
| `consumer_op_failed` | Различные | Subscribe / consumer-операции |
| `publish_failed` | Unavailable | Failure публикации JetStream / NATS |
| `encode_failed` / `decode_failed` | Internal | Codec-failures |

## OpenTelemetry trace-propagation

Каждый publish-путь (`Publisher.Publish`, `PublishViaCodec`, `PublishRaw`) инжектит W3C `traceparent` / `tracestate` headers в outbound msg, используя process-global OTel propagator (no-op, когда ни один не установлен). Каждый subscribe-путь (`Subscribe`, `SubscribeRaw`) экстрактит их обратно в handler'ский `ctx`, так что handler-span'ы — дети publisher-span'а. Результат: водопад Sentry / Jaeger / Tempo показывает HTTP → NATS publish → NATS handle как одну непрерывную трассу через async-границу.

```go
otel.SetTextMapPropagator(propagation.TraceContext{}) // кит не устанавливает один за вас
```

Путь `Inject` **идемпотентен по существующему `traceparent`**: когда headers-map уже несёт его (типично для `db/outbox`-flow, где оригинальный TraceContext запроса был снапшочен в строку на Enqueue-time), propagator НЕ перезаписывает. Это держит более поздний dispatch worker'а outbox'а на originating-трассе, а не на свежей worker-loop-трассе.

Для batched JetStream Pull-пути (`natsmap` с `batch_size: N`), dispatch ctx — `context.Background()` (батч может смешивать трассы — выбирать одну неправильно). Handler'ы, итерирующие per-msg, могут extract'ить per-event: `ctx = natsclient.ExtractTraceContext(ctx, msg.Headers)`.

## Observability

### slog

- `Info "natsclient connect"` на первом connect'е
- `Warn "natsclient disconnected"` на каждом disconnect'е
- `Info "natsclient reconnected"` на каждом успешном reconnect'е
- `Warn "natsclient handler error"` (на Nak'е)
- `Error "natsclient decode failed"` (poison pill Term)
- `Warn "natsclient max deliver exceeded"` (Term после retries)

### Prometheus (опционально)

| Метрика | Тип | Labels |
|---|---|---|
| `natsclient_published_total` | counter | `subject`, `status` |
| `natsclient_publish_duration_seconds` | histogram | `subject` |
| `natsclient_handled_total` | counter | `subject`, `result` (`ack`/`nak`/`term`) |
| `natsclient_handler_duration_seconds` | histogram | `subject` |
| `natsclient_in_flight` | gauge | `subject` |

## Тестирование

Используйте [testcontainers-go/modules/nats](https://golang.testcontainers.org/modules/nats/):

```go
c, _ := tcnats.Run(ctx, "nats:2-alpine", testcontainers.WithCmd("-js"))
t.Cleanup(func() { _ = c.Terminate(ctx) })
url, _ := c.ConnectionString(ctx)

client, _ := natsclient.Connect(ctx, natsclient.Config{URL: url, Name: "test"})
defer client.Close()
client.EnsureStream(ctx, natsclient.StreamConfig{
    Name: "TEST", Subjects: []string{"test.>"},
})

// subscribe + publish + assert
```

Для per-test изоляции используйте уникальные stream + subject имена per test.

## Escape hatches — `Conn()` / `JetStream()`

`Client.Conn()` и `Client.JetStream()` возвращают сырые `*nats.Conn` и `nats.JetStreamContext` — для advanced-операций, которые кит пока не оборачивает (raw subscriptions, custom request/reply, consumer-config tuning, stream snapshots, `AccountInfo`).

**Это passthrough.** Кит **не** надевает свои observability- и resilience-слои поверх direct-вызовов:

| Слой | Через `Publish` / `PublishViaCodec` / `PublishRaw` / `Subscribe` | Через `Conn()` / `JetStream()` напрямую |
|---|---|---|
| `*errs.Error` mapping | ✓ | ✗ — caller владеет error-mapping'ом |
| Prometheus `nats_*` collector'ы | ✓ | ✗ — direct-вызовы не observ'ятся |
| Breaker / default-timeout | ✓ | ✗ |
| W3C TraceContext инжекция | ✓ | ✗ — caller вызывает `natsclient.InjectTraceContext` сам, если нужно |

**Lifecycle.** Кит owns `Close` — caller'ы **MUST NOT** делать `c.Conn().Close()` / `c.Conn().Drain()` напрямую. Это bypass'ит идемпотентный `Client.Close` и оставляет внутреннее состояние kit'а out-of-sync. Закрытие — через `Client.Close` (или через `service.Close` если bundle'нут).

**v1 contract.** Signature'ы `Conn()` и `JetStream()` остаются stable и кит **не планирует** начинать wrapping в существующих методах — "passthrough" это контракт, не имплементационная случайность. Если нужен behaviour, которого кит не expose'ит — лучше открыть issue / PR на новый typed method, чем строить hot-path-инфраструктуру на этих hatch'ах.

`JetStream()` возвращает `nil` в core-only режиме (когда Connect был без jetstream-opt'ов). Nil-check перед использованием.

## Ограничения

- **JetStream-first дизайн.** Subjects, не покрытые никаким stream'ом, авто-используют core NATS (best-effort). Если вам нужен исключительно core NATS, используйте сырой `nats.go` и пропустите эту обёртку.
- **Один codec на Client.** Гетерогенные wire-format'ы по topic'ам требуют нескольких инстансов `*Client` или кастомного codec'а, диспатчящего внутри.
- **Auto-ack model — opinionated.** Handler возвращает nil → Ack. Никаких "explicit Ack позже из горутины" — by design (избегает leaked Ack budget).
- **`WithMaxInFlight` local, не stream-wide.** Для stream-wide backpressure'а используйте собственный MaxAckPending у JetStream'а на consumer'е.
- **Нет multi-stream subjects через один Subscribe.** Одна subscription = один subject (или NATS-wildcard) на одном stream'е.
- **`Drain` блокирует** пока in-flight handlers не закончат. Для force-shutdown используйте `Close` (теряет in-flight).

## См. также

- [`errs`](../../errs/README.md) — error-контракт
- [`examples/nats`](../../examples/nats/) — минимальный publish + subscribe пример
- [`examples/urlshort`](../../examples/urlshort/README.md) — использует natsclient для публикации `urlshort.link.{created,visited}`
</content>
