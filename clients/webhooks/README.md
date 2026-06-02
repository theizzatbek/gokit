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

## Что НЕ входит в v1

- per-target circuit breaker (отдельный спек),
- admin REST endpoints для CRUD subscriptions,
- Stripe / Telegram / Slack verifiers,
- redis-реализация SubscriptionStore,
- per-subscription retry policy,
- ротация encryption-ключа на write.

## Тестирование

- Unit: `go test ./clients/webhooks/...` (Signer, Verifier, Fanout с fake stores, outcome classify, backoff).
- Integration: `go test ./clients/webhooks/storepg/...` (требует Docker для testcontainers postgres).
- `webhookguard`: `go test ./fibermap/webhookguard/` (in-process Fiber).
