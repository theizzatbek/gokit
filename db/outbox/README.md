# db/outbox

Transactional-outbox pattern for Postgres-backed kit services. Events get
written to an `outbox` table inside the same transaction as the business
state; a background `Worker` dispatches them to the real bus (NATS / Kafka)
with at-least-once delivery.

## Why use it

Without an outbox, services that "commit then publish" have a crash
window between the two. The DB row is durable; the event isn't. A
restart between commit and publish means downstream consumers miss the
event forever. The outbox closes this window by making the publish step
part of a different, retryable transaction.

Use this package when you need any of:

- Linking DB state changes to downstream pub/sub events without losing
  either one to a crash.
- At-least-once delivery on top of any pub/sub system (the package is
  publish-agnostic — caller supplies the `PublishFn`).
- Multi-replica safe drainer (`SELECT ... FOR UPDATE SKIP LOCKED`
  baked into the worker query).

## Quickstart

Apply the schema (migration runner is out of scope — the kit ships the
DDL):

```go
//go:embed migrations/*.sql
var migrationsFS embed.FS

// ... migrate via golang-migrate or run outbox.Schema() once at boot:
_, _ = svc.DB.Exec(ctx, outbox.Schema())
```

Enqueue inside the surrounding transaction:

```go
err := svc.DB.Tx(ctx, func(tx *db.Tx) error {
    if _, err := svc.LinksRepo.Create(ctx, tx, link); err != nil {
        return err
    }
    payload, _ := json.Marshal(linkCreated)
    return outbox.Enqueue(ctx, tx, outbox.Event{
        AggregateType: "link",
        AggregateID:   link.Code,
        EventType:     "urlshort.link.created",
        Payload:       payload,
    })
})
```

Drain with a worker:

```go
w, _ := outbox.NewWorker(svc.DB, func(ctx context.Context, e outbox.Event) error {
    return natsmap.PublishRaw(ctx, svc.NATSMap, e.EventType, e.Payload, e.Headers)
},
    outbox.WithInterval(5*time.Second),
    outbox.WithBatchSize(100),
    outbox.WithLogger(svc.Logger()),
)
_ = w.Start(ctx)
svc.OnShutdown(w.Stop)
```

## Schema

`schema.sql` defines a single `outbox` table with a partial index over
the unpublished set:

```sql
CREATE INDEX outbox_unpublished_created_at_idx
    ON outbox (created_at)
    WHERE published_at IS NULL;
```

The partial index keeps the polling SELECT fast even after millions of
delivered rows accumulate. Drop old rows from a periodic cron — the
worker doesn't garbage-collect.

## Worker semantics

- **Polling**: `SELECT ... WHERE published_at IS NULL ORDER BY created_at
  LIMIT $batch_size FOR UPDATE SKIP LOCKED`. Multi-replica safe — two
  workers draining the same table don't collide.
- **Per-event dispatch**: `PublishFn(ctx, Event) error`. Returning nil
  marks the row published; returning an error bumps `attempts` and
  records the message in `last_error`.
- **Retries**: unbounded by default — failed events stay in the
  unpublished set and the worker retries them on the next tick. Cap
  with `WithMaxAttempts(n)`.
- **Dead-lettering**: rows whose `attempts >= max_attempts` stay in
  the table but are filtered out of the SELECT. Operators decide the
  disposition (delete, replay, archive).
- **At-least-once contract**: a crash AFTER `PublishFn` succeeds but
  BEFORE the row's UPDATE will redeliver. Downstream consumers must
  dedupe — set the bus's `Nats-Msg-Id` (or equivalent) to `Event.ID`.

## Options

| Option | Default | Notes |
|---|---|---|
| `WithInterval(d)` | 5s | Polling cadence. The worker fires the first fetch immediately so events Enqueued just before Start land without waiting. |
| `WithBatchSize(n)` | 100 | Max events fetched per tick. Larger amortises round-trips; locks held longer. |
| `WithMaxAttempts(n)` | 0 (no cap) | Dead-letter rows whose attempt count reaches n. Stays in table for operator. |
| `WithLogger(*slog.Logger)` | silent | Debug per successful batch, Warn per publish failure, Error per drain failure. |

## Error codes

| Code | Returned by | Meaning |
|---|---|---|
| `outbox_enqueue_failed` | `Enqueue` | Underlying INSERT failed. Surrounding transaction MUST roll back. |
| `outbox_missing_fields` | `Enqueue` | `Event.EventType` is empty. |
| `outbox_marshal_headers` | `Enqueue` | Headers map could not be JSON-encoded (cyclic / NaN). |
| `outbox_worker_nil_db` | `NewWorker` | `NewWorker(nil, fn)`. |
| `outbox_worker_nil_publish_fn` | `NewWorker` | `NewWorker(db, nil)`. |
| `outbox_worker_started` | `Start` | Second `Start` call — worker is single-use. |

## Testing

`outbox_test.go` runs against testcontainers Postgres. Covered scenarios:

- `Enqueue` inserts the expected row.
- `Enqueue` inside a rolled-back transaction does NOT persist (the
  consistency guarantee).
- Worker drains a backlog under a tight poll interval.
- Worker retries failed publishes; the row's `attempts` bumps and the
  worker eventually succeeds when the function returns nil.
- `WithMaxAttempts` caps retries — the row stays in the table but no
  longer dispatches.
- `Start` is single-use — the second call returns an error.

Run with `go test ./db/outbox/...` (skips under `-short`).

## See also

- [`db`](../README.md) — the underlying pool wrapper
- [`clients/natsmap`](../../clients/natsmap/README.md) — typed NATS publish surface; pair with `natsmap.PublishRaw` for outbox-style flows
