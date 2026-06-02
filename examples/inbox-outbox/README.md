# examples/inbox-outbox

Single-process демо kit'овой **effectively-once event flow**: producer commits
domain row + outbox row в одной Tx, outbox worker draining через
`outboxnats.NewPublisher`, consumer wraps handler через
`inboxnats.Wrap` для dedup'а redelivery.

## Запуск

```bash
go run ./examples/inbox-outbox
```

Требуется только Docker — testcontainers поднимает Postgres + NATS прямо в
процессе (~10s startup, автоматический cleanup).

## Что показывает

### Producer side (transactional outbox)

```go
func producerCommit(ctx, d, evt) error {
    return d.Tx(ctx, func(tx *db.Tx) error {
        // (1) Domain write.
        tx.Exec(ctx, "INSERT INTO orders_producer_view ...")
        // (2) Outbox enqueue INSIDE the SAME Tx — single commit.
        return outbox.EnqueueTyped(ctx, tx, "orders.created", evt)
    })
}
```

Crash window "commit-then-publish" не существует — оба row'а атомарны.
Outbox worker (`outboxnats.NewPublisher(rt)`) асинхронно дренит таблицу в
NATS.

### Consumer side (effectively-once)

```go
natsmap.RegisterHandler[json.RawMessage](eng, "orders-sink",
    inboxnats.Wrap[json.RawMessage]("demo:orders-consumer", db,
        func(ctx, tx *db.Tx, m natsclient.Msg[json.RawMessage]) error {
            // tx — это inbox-Tx; здесь же inbox row commit'нется.
            tx.Exec(ctx, "INSERT INTO orders ...")
            return nil
        }))
```

При redelivery того же `Nats-Msg-Id`:

1. `inboxnats.Wrap` извлекает `Nats-Msg-Id` из headers.
2. Открывает Tx, делает `INSERT ... ON CONFLICT DO NOTHING` в inbox-таблице.
3. Если row уже есть (`RowsAffected==0`) → return nil **без вызова user fn**.
4. `natsmap` видит nil err → Ack'ает duplicate без re-running side effect.

## Output

```
→ Starting Postgres + NATS containers (~10s)…

→ Producer commits 5 orders (domain row + outbox row in one Tx):
INFO producer enqueued order_id=order-1
INFO producer enqueued order_id=order-2
... (5 total)

→ Forcing a duplicate redelivery of order-1 (inboxnats must dedup):
INFO consumer applied order order_id=order-1
INFO consumer applied order order_id=order-2
... (5 total — НЕ 6, потому что duplicate был skip'нут)

→ Waiting for consumer to drain…

=== Final state ===
  producer rows (orders_producer_view): 5   (expected 5)
  consumer rows (orders):               5   (expected 5)
  inbox rows (consumer namespace):      5   (expected 5 — duplicate not stored twice)
  unpublished outbox rows:              0   (expected 0 — worker drained them)
  handler closure invocations:          5   (expected 5 — dedup skipped the duplicate)

OK — effectively-once boundary holds.
```

## Ключевые точки

- **Crash window закрыт с обеих сторон.** Producer не теряет publish при
  crash между db.Commit и nats.Publish. Consumer не выполняет side effect
  дважды при redelivery.
- **Adapter packages дают ergonomics.** Producer = 1 строка `outboxnats.NewPublisher(rt)`. Consumer = 1 строка `inboxnats.Wrap(consumer, db, fn)`. Без них caller писал бы closure-PublishFn + manual `inbox.Process` в каждом handler'е.
- **DB-side exactly-once.** Side effects ВНЕ DB (третьи HTTP, файлы) НЕ
  покрыты — для них нужна отдельная idempotency (см. `fibermap.IdempotencyKey`).

## Что НЕ покрывает

- **Multi-replica retention.** Demo запускается один раз — реальный сервис
  должен или запустить inbox `RetentionWorker` на одной реплике, или
  обернуть `Tick` через `db/lock` для leader-elect.
- **Partial failures с retry budget.** Outbox worker retry'ит per-event с
  exponential backoff (default 8 attempts). Демо не симулирует выход за
  лимит — это уже spec'овый scenario.

## См. также

- [`db/outbox`](../../db/outbox/README.md) — outbox table + Worker.
- [`db/outbox/outboxnats`](../../db/outbox/outboxnats/README.md) — natsmap adapter.
- [`db/inbox`](../../db/inbox/README.md) — inbox table + Process + retention.
- [`db/inbox/inboxnats`](../../db/inbox/inboxnats/README.md) — natsmap handler wrapper.
- [`clients/natsmap`](../../clients/natsmap/README.md) — declarative NATS pub/sub.
