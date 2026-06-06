# clients/webhooks

Outbound + inbound HTTP webhook'и для kit'а.

- **Outbound:** `Subscription` + `Delivery` хранятся в Postgres (`storepg`), `Fanout` превращает один domain event в N deliveries, `Worker` доставляет с подписью HMAC-SHA256 и per-target retry/backoff/DLQ.
- **Inbound:** `Verifier` interface + готовые реализации в `verifiers/` (GenericHMAC, GitHub); монтируется как middleware `fibermap/webhookguard`.

## Архитектура

```
[business Tx]
  ├── INSERT (business)
  └── outbox.Enqueue(tx, "link.created", payload)        // как сейчас

[outbox.Worker]
  └── publish on NATS subject="link.created"

[your natsmap handler]
  └── svc.WebhooksFanout.HandleEvent(ctx, Event{...})
       ├── SubStore.ListByEvent("link.created") → [sub1, sub2, sub3]
       └── DB.Tx: DeliveryStore.Enqueue(tx, []Delivery{...})

[DeliveryWorker]
  ├── DeliveryStore.Claim(N)  // JOIN webhook_subscriptions, decrypt secret
  └── per delivery (bounded by MaxInFlight):
       ├── Sign(body, secret, now) → "X-Webhook-Signature: t=<unix>,v1=<hex>"
       ├── httpc.Do(POST target_url, body, headers)
       ├── on 2xx → MarkDelivered
       └── on retryable → MarkFailed(next = now + backoff)
```

## Подпись (outbound)

```
X-Webhook-Signature:  t=<unix>,v1=<hex(hmac-sha256(t + "." + body, secret))>
X-Webhook-Delivery:   <delivery_id>
X-Webhook-Event-Type: <event_type>
```

Принимающая сторона проверяет: `HMAC-SHA256(t + "." + body, secret)` и `|now - t| < 5min`.

## Минимальный outbound

```go
secretKey, _ := base64.StdEncoding.DecodeString(os.Getenv("WEBHOOKS_SECRET_KEY"))
subStore, _ := storepg.NewSubStore(svc.DB, secretKey)
delivStore, _ := storepg.NewDeliveryStore(svc.DB, secretKey)

svc, _ := service.New[App, Claims](ctx, cfg,
    service.WithWebhooks(service.WebhooksConfig{
        SubStore:      subStore,
        DeliveryStore: delivStore,
        StartWorker:   true,
        StartFanout:   true,
    }),
    service.WithNATSMapRegistration(func(eng *natsmap.Engine) {
        natsmap.RegisterHandler[LinkCreated](eng, "link.created",
            func(ctx context.Context, msg natsclient.Msg[LinkCreated]) error {
                return svc.WebhooksFanout.HandleEvent(ctx, webhooks.Event{
                    ID:        msg.Headers().Get("Nats-Msg-Id"),
                    EventType: msg.Subject(),
                    Payload:   msg.RawData(),
                })
            })
    }),
)
```

## Минимальный inbound

```go
v := verifiers.NewGitHub([]byte(os.Getenv("GITHUB_WEBHOOK_SECRET")))
app.Post("/inbound", webhookguard.New(v), handler)
```

## Storage

`storepg` шифрует `Subscription.Secret` через AES-256-GCM ключом `WEBHOOKS_SECRET_KEY` (32 байт base64). При отсутствии ключа `NewSubStore` возвращает `*errs.Error{Code: webhooks_storepg_no_key}`.

Формат `secret_enc`: `[версия(1 байт)=0x01][nonce(12 байт)][ciphertext+tag]`. Версия позволяет ротировать ключ в будущем без миграции данных.

DLQ-строки (`status='dlq'`) НЕ удаляются retention'ом — инспектируются оператором.

## Retention

```go
r := webhooks.NewRetentionWorker(webhooks.RetentionConfig{
    DB:  svc.DB,
    TTL: 30 * 24 * time.Hour, // delivered старше TTL удаляются
})
r.Start(ctx)
svc.OnShutdown(func() error { return r.Stop(context.Background()) })
```

## Outcome classification (Worker)

| HTTP status | Действие |
|---|---|
| 2xx | `MarkDelivered` |
| 5xx, 408, 429, network/timeout | `MarkFailed` с backoff; после MaxAttempts → DLQ |
| 4xx (≠ 408, ≠ 429) | сразу `MarkDLQ` (клиент сказал «не пробуй снова») |

Default backoff: `Initial=1s, Max=1h, Multiplier=2.0`. С MaxAttempts=8 — ~4 минуты живого retry.

### Custom classifier

`WorkerConfig.RetryClassifier` override'ит default-правило. `webhooks.DefaultClassifier` экспортирован как building block:

```go
cfg.RetryClassifier = func(resp *http.Response, err error) webhooks.Outcome {
    if resp != nil && resp.StatusCode == 422 {
        return webhooks.OutcomeRetryable // upstream rate-limits через 422
    }
    return webhooks.DefaultClassifier(resp, err)
}
```

## Per-subscription circuit breaker

```go
cfg.BreakerFactory = func(subID uuid.UUID) *breaker.Breaker {
    b, _ := breaker.New(breaker.Config{
        Name:             "wh:" + subID.String(),
        FailureThreshold: 10,
        MinimumRequests:  20,
    })
    return b
}
```

Open-state на конкретный subscription ID → attempt short-circuit'ится как retryable; delivery reschedule'ится по backoff curve, не сжигая in-flight slot на known-down endpoint. Один битый subscriber не валит worker.

## Lifecycle hooks

```go
cfg.OnAttempt = func(d webhooks.Delivery, resp *http.Response, err error, outcome webhooks.Outcome, elapsed time.Duration) {
    auditLog.Record(d.SubscriptionID, d.EventType, outcome.String(), elapsed)
}
cfg.OnDLQ = func(d webhooks.Delivery, status int, msg string) {
    slack.Notify("delivery dropped to DLQ", d.ID, status, msg)
}
```

`OnAttempt` фа́ерит на каждой попытке (success / failure); `OnDLQ` — когда delivery попадает в DLQ. Hooks recover'ятся от panic'ов (best-effort).

## Custom signature scheme

`WorkerConfig.SignerFunc` swap'ит Stripe-style на app-specific:

```go
cfg.SignerFunc = func(body []byte, secret string, now time.Time) (string, error) {
    return "X-MyApp-Sig-v2: " + customHMAC(body, secret), nil
}
```

Дефолт — Stripe-style `t=<unix>,v1=<hmac>` через `webhooks.Signer.Sign`.

## TraceContext propagation

```go
cfg.Propagator = otel.GetTextMapPropagator()
```

`WorkerConfig.Propagator` — **единственная** точка инъекции tracing-заголовков. Worker делает ровно `Propagator.Inject(ctx, req.Header)` один раз на attempt; всё что propagator пишет, попадает на провод verbatim.

- **Default (`nil`)** — никаких tracing-заголовков. Kit не fall-back'ает на `otel.GetTextMapPropagator()` под капотом, не подмешивает свой `traceparent`, не читает global state.
- **Рекомендация — OTel composite** (`otel.GetTextMapPropagator()` по умолчанию): W3C TraceContext (`traceparent` / `tracestate`) + W3C Baggage. Это shape, который receiver ожидает в kit-овских примерах и который продуцируют `otelhttp` и `clients/nats`.
- **Кастомные propagator'ы (B3 / Jaeger / Datadog)** работают так же — kit просто передаёт байты, ничего не интерпретирует. Receiver-side compatibility — на стороне caller'a.
- **v1 contract**: этот field — **единственная стабильная точка** контроля tracing-инъекции на Worker. Kit НЕ добавит side-channel поля (`WorkerConfig.B3Propagator`, `WorkerConfig.DatadogHeaders`, …) в v1 minor'ах. Для мульти-формата — собирайте `propagation.NewCompositeTextMapPropagator(p1, p2, …)` и передавайте composite сюда.

## Прочие knobs

| Поле | По умолчанию | Заметки |
|---|---|---|
| `AttemptTimeout` | 30s | Per-attempt HTTP timeout. |
| `DefaultContentType` | `application/json` | Override для protobuf / raw payload (`Delivery.Headers["Content-Type"]` всё ещё win'ит). |

## Healthcheck

```go
chk := webhooks.NewChecker(delivStore, "webhooks")
// Pass to fibermap.Readiness alongside db / nats / redis checkers.
```

`Check(ctx)` пингует `DeliveryStore.Claim(ctx, 0)` — zero-batch claim не модифицирует state, но доказывает что store reachable. Nil-receiver safe → `webhooks_not_ready` вместо panic.

## Resilience defaults

- Panic recovery в attempt goroutine — recovered + scheduled retry. Slot не утекает.
- Per-subscription breaker (когда сконфигурирован) — known-down sub не блокирует другие.
- Hook panic'и recover'ятся — broken audit не валит delivery.

## Тестирование

- Unit: `go test ./clients/webhooks/...` (Signer, Verifier, Fanout с fake stores, outcome classify, backoff).
- Integration: `go test ./clients/webhooks/storepg/...` (требует Docker для testcontainers postgres).
- `webhookguard`: `go test ./fibermap/webhookguard/` (in-process Fiber).
