# db/notify

Goroutine-safe Postgres LISTEN/NOTIFY helper. The kit's outbox v2
uses the same pattern internally; this package exposes it as a
general-purpose primitive for the broader set of pub/sub-over-pg use
cases:

- Cache invalidation broadcast (Postgres trigger `pg_notify` → app
  drops local cache).
- Materialized view refresh signals.
- Distributed locks notifications.
- Real-time projection updates.

## Why use it

LISTEN/NOTIFY by itself requires a dedicated connection, careful
reconnect on conn drop, and a handler dispatch loop. Every service
that uses it reinvents these. This package gives you:

- Dedicated pool conn held for the notifier's lifetime.
- Bounded-backoff reconnect on conn drop or LISTEN failure.
- Single-goroutine handler dispatch in receipt order.
- `Start` / `Stop` lifecycle matching the kit's conventions.

## Quickstart

```go
n := notify.NewNotifier(svc.DB, []string{"cache_invalidate"},
    func(ctx context.Context, n notify.Notification) error {
        cache.Drop(n.Payload)
        return nil
    },
    notify.WithLogger(svc.Logger()),
)
_ = n.Start(ctx)
svc.OnShutdown(n.Stop)

// Anywhere else (same or different process):
_, _ = svc.DB.Exec(ctx, `SELECT pg_notify('cache_invalidate', $1)`, key)
```

## API surface

| Symbol | Notes |
|---|---|
| `NewNotifier(d, channels, handler, opts...)` | Construct. Channels must be valid Postgres identifiers (`[A-Za-z_][A-Za-z0-9_]*`). |
| `(*Notifier).Start(ctx)` | Spawns the listen goroutine. Idempotent. |
| `(*Notifier).Stop()` | Cancels ctx + waits for the goroutine. Idempotent + nil-safe. |
| `notify.WithLogger(l)` | Wire a slog.Logger for lifecycle + per-notification diagnostics. |
| `notify.Notification` | `{Channel, Payload string}` — what your handler receives per `pg_notify` call. |

## Semantics

- **Connection isolation**: the notifier holds ONE `*pgxpool.Conn`
  for its lifetime. Pool `MaxConns >= 2` recommended so foreground
  queries don't starve.
- **No durability**: NOTIFY is fire-and-forget. Notifications sent
  during the reconnect window are LOST. Callers that need
  durability should pair this with a recovery mechanism — e.g. a
  SELECT against an indexed table on reconnect to drain anything
  missed.
- **Single-goroutine handler**: notifications dispatch in receipt
  order, one at a time. Blocking the handler queues subsequent
  notifications at the server-side buffer. Fan out to a worker
  pool from inside the handler for high-throughput sources.
- **Handler errors**: logged at Warn (when WithLogger is set) and
  ignored. Postgres has no nak/redeliver primitive — the operator
  has to instrument retry separately.

## Comparison with `db/outbox`

The outbox uses LISTEN/NOTIFY internally for the worker's wake-up
path. The two pieces serve different needs:

| | `db/outbox` | `db/notify` |
|---|---|---|
| **Durability** | At-least-once via Postgres rows. | Fire-and-forget — no DB state. |
| **Use case** | Transactional event publish to a real bus (NATS / Kafka). | App-internal real-time signals. |
| **Sender** | `outbox.Enqueue` inside the business Tx. | Plain `pg_notify(channel, payload)` from anywhere. |

Both can coexist — the outbox uses its own channel name
(`outbox_new`), distinct from anything you'd register through
`notify.NewNotifier`.

## See also

- [`db`](../README.md) — the underlying pool wrapper.
- [`db/outbox`](../outbox/README.md) — durable transactional outbox; uses the same LISTEN pattern internally.
- Postgres docs on [LISTEN](https://www.postgresql.org/docs/current/sql-listen.html) / [NOTIFY](https://www.postgresql.org/docs/current/sql-notify.html).
