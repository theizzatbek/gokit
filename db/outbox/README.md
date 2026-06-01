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

The shortest path — let `service.New` wire everything:

```go
svc, _ := service.New[Ctx, Claims](ctx, cfg,
    service.WithNATSMap(),
    service.WithOutbox(
        outbox.WithRetention(7*24*time.Hour),
    ),
    service.WithOutboxAutoSchema(), // applies schema.sql idempotently
)
```

In the domain service, enqueue inside the surrounding transaction
via the typed sugar:

```go
err := svc.DB.Tx(ctx, func(tx *db.Tx) error {
    if _, err := svc.LinksRepo.Create(ctx, tx, link); err != nil {
        return err
    }
    return outbox.EnqueueTyped(ctx, tx, "urlshort.link.created",
        events.LinkCreated{LinkID: link.ID, Code: link.Code, ...},
        outbox.WithAggregate("link", link.Code))
})
```

For non-`service.New` callers, use the building blocks directly:

```go
_, _ = svc.DB.Exec(ctx, outbox.Schema())            // apply DDL
w, _ := outbox.NewWorker(svc.DB,
    func(ctx context.Context, e outbox.Event) error {
        return natsmap.PublishRaw(ctx, svc.NATSMap,
            e.EventType, e.Payload, e.Headers)
    },
    outbox.WithLogger(svc.Logger()),
    outbox.WithMetrics(reg),
    outbox.WithRetention(7*24*time.Hour),
)
_ = w.Start(ctx)
svc.OnShutdown(w.Stop)
```

## Readiness check

`outbox.NewChecker(d, opts...)` is the [`fibermap.Checker`](../../fibermap/README.md) implementation that surfaces outbox backlog on `/readyz`. `service.WithOutbox` auto-adds it; tune via `service.WithOutboxReadinessOpts(...)` or disable via `service.WithoutOutboxReadiness()`.

| Option | Default | Notes |
|---|---|---|
| `WithMaxDepth(n)` | 10000 | Pending row count above this → 503 + `outbox_backlog` code. |
| `WithMaxLag(d)` | 10m | Oldest pending row's age above this → 503 + `outbox_backlog` code. |
| `WithCheckerName(name)` | "outbox" | Name surfaced under `checks: {…}` in the 503 body. |

The check runs `SELECT count(*), MIN(created_at)` against the partial index the worker already uses — no extra index required.

## Trace context

`Enqueue` snapshots the current OTel `TraceContext` (W3C `traceparent` / `tracestate`) into `Event.Headers` so the Worker's later publish preserves the originating trace across the async boundary. The kit's `natsclient.PublishRaw` inject path treats a pre-existing `traceparent` in headers as authoritative — the worker's (usually trace-less) ctx never overwrites the snapshot. Result: HTTP → Tx → outbox row → Worker → consumer all share one trace ID in the APM waterfall.

No setup required — propagation activates automatically once a `TextMapPropagator` is installed globally (`otel.SetTextMapPropagator(propagation.TraceContext{})`, which `service.WithOtel` does).

## Schema (v2)

`schema.sql` is **idempotent** for both fresh installs and v1 → v2
upgrades. Columns:

| Column | Notes |
|---|---|
| `id uuid PRIMARY KEY` | DB-generated UUID — surfaces in `Event.ID`. |
| `aggregate_type, aggregate_id text` | Optional aggregate-shape labels. |
| `event_type text NOT NULL` | Bus subject the Worker dispatches to. |
| `payload bytea NOT NULL` | Opaque wire bytes — JSON, protobuf, anything. |
| `headers jsonb` | Per-event metadata (W3C traceparent, etc). |
| `created_at, published_at timestamptz` | Lifecycle stamps. |
| `attempts integer, last_error text` | Retry bookkeeping. |
| `next_retry_at timestamptz NOT NULL DEFAULT NOW()` | **v2** — per-row backoff "ready at". |

Indexes:

- `outbox_pending_idx (next_retry_at, created_at) WHERE published_at IS NULL` — the polling SELECT touches only rows whose retry window has arrived.
- `outbox_aggregate_idx (aggregate_type, aggregate_id)` — for replay tooling.

Use `outbox.Schema()` once at boot (or fold into your migration tool); `service.WithOutboxAutoSchema()` does this automatically.

## Worker semantics

- **Polling**: `SELECT ... WHERE published_at IS NULL AND next_retry_at <= NOW() ORDER BY next_retry_at, created_at LIMIT $batch_size FOR UPDATE SKIP LOCKED`. Multi-replica safe — two workers draining the same table don't collide.
- **LISTEN/NOTIFY fast path** (v2, default-on): a dedicated pool connection LISTENs on `outbox_new`; Enqueue runs `pg_notify('outbox_new', '')` after INSERT, so commit-to-publish latency is ~ms instead of waiting for the next polling tick. Polling stays as the fallback for crash recovery / dropped NOTIFY. Disable via `WithoutListen()` when running behind a connection pooler that breaks NOTIFY (PgBouncer transaction mode).
- **Per-row exponential backoff** (v2): on failure, `next_retry_at = NOW() + base * 2^(attempts-1)` capped at max. Defaults: base 1s, max 1h. Stops failed events from hammering the bus every poll tick.
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
| `WithInterval(d)` | 5s | Polling cadence (LISTEN/NOTIFY usually wakes the worker first; this is the fallback). |
| `WithBatchSize(n)` | 100 | Max events fetched per tick. |
| `WithMaxAttempts(n)` | 0 (no cap) | Dead-letter rows whose attempt count reaches n. |
| `WithBackoff(base, max)` | 1s, 1h | Per-row exponential retry timing. Pass `(0, 0)` to disable. |
| `WithoutListen()` | listen on | Disable LISTEN/NOTIFY. Polling-only mode. |
| `WithRetention(d)` | off | GC published rows older than d. |
| `WithGCInterval(d)` | 1h | Retention sweep cadence (no-op without `WithRetention`). |
| `WithLogger(*slog.Logger)` | silent | Debug / Warn / Error per lifecycle event. |
| `WithMetrics(prometheus.Registerer)` | off | Register `outbox_events_total{outcome}` (counter), `outbox_publish_duration_seconds` (histogram), `outbox_pending_count` (gauge), `outbox_gc_deleted_total` (counter), `outbox_listen_wakes_total` (counter). |

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
