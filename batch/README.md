# batch

Generic batched-handler dispatcher. Collects typed items via
`Submit` and hands them to the caller's `HandlerFn` as a slice — one
call per buffered batch. Per-item ack callbacks let upstream
sources (e.g. the kit's natsmap pull subscriber) commit the whole
batch atomically.

**Import:** `github.com/theizzatbek/gokit/batch`
**Depends on:** `github.com/prometheus/client_golang/prometheus`, `errs/`

## When to use

- Want one handler call per N items + every-item-or-none
  acknowledgement.
- Buffering an event stream into a bulk DB write
  (`INSERT/UPDATE … VALUES (..), (..), …`).
- Throttling outbound batches (one HTTP push per second instead of
  N pushes per second).

For visit-counter-style aggregation where you want a key-keyed map
collapsed before flush (sum across many events into one row per
key), do the aggregation **inside** `HandlerFn` — that's a domain
concern, not a kit concern.

## Quickstart — direct use

```go
b, err := batch.New[Event](batch.Config[Event]{
    HandlerFn: func(ctx context.Context, batch []Event) error {
        return persistAll(ctx, batch) // one transaction
    },
    BatchSize: 1000,        // required
    Interval:  time.Second, // default 1s when zero
    Logger:    logger,
})
if err != nil { return err }
defer b.Close()

for evt := range events {
    b.Submit(evt, nil) // fire-and-forget
}
```

## Quickstart — with per-item ack

```go
b.Submit(event, func(err error) {
    if err == nil {
        msg.Ack()          // upstream queue commit
    } else {
        msg.Nak()          // redeliver on err
    }
})
```

Every `ack` closure in a batch fires with the same `err` —
`HandlerFn`'s return value. All-or-nothing semantics: a successful
batch ack-confirms every item; a failed batch nak-redelivers every
item.

The kit's `natsmap.RegisterBatchedHandler` uses exactly this pattern
internally so YAML-declared subscribers get at-least-once batched
delivery without users wiring Submit themselves.

## Config

| Field | Default | Notes |
|---|---|---|
| `HandlerFn func(ctx, []T) error` | — | **Required.** Receives the buffered slice as one call. |
| `BatchSize int` | — | **Required, > 0.** Size cap; reaching it fires an early flush. |
| `Interval time.Duration` | `1s` | Buffer-age cap. Either trigger flushes. |
| `Logger *slog.Logger` | nil (silent) | Warn entries on HandlerFn errors. |
| `Metrics prometheus.Registerer` | nil (off) | Four `batch_*` collectors. |

## API

```go
func New[T any](cfg Config[T]) (*Batcher[T], error)

func (b *Batcher[T]) Submit(item T, ack func(err error))
func (b *Batcher[T]) Flush(ctx context.Context) error
func (b *Batcher[T]) Close() error
```

- `Submit` is goroutine-safe — producer goroutines call it
  concurrently. `nil` ack is supported for fire-and-forget items.
- `Flush` is a manual drain (tests, interactive shutdown paths).
- `Close` does one final flush, stops the goroutine, is idempotent.
- `(*Batcher[T])(nil)` is safe on every method — Submit is a no-op,
  Flush/Close return nil. Lets callers thread an optional batcher
  through their code.

## Trigger model

Two triggers run in parallel:

1. **Interval ticker** — every `Interval`.
2. **Size cap** — `Submit` brings the buffer to `BatchSize` distinct
   items, a non-blocking signal jumps the queue and triggers an
   immediate flush.

Both call the same dispatch path; `HandlerFn` runs **outside** the
batcher's lock so high-frequency `Submit` calls don't stall on a
slow downstream.

## Errors

`New` returns `*errs.Error` via `errors.Join` when required fields
are missing — every gap surfaces at once.

| Code | Cause |
|---|---|
| `batch_missing_handler_fn` | `Config.HandlerFn` nil |
| `batch_invalid_batch_size` | `Config.BatchSize` <= 0 |

## Metrics

Pass `Config.Metrics = reg` to register four series:

| Series | Type | Labels |
|---|---|---|
| `batch_handlers_total` | Counter | `outcome=success\|error` |
| `batch_items_processed_total` | Counter | — |
| `batch_handler_duration_seconds` | Histogram | — |
| `batch_batch_size` | Histogram | — |

## See also

- [`clients/natsmap`](../clients/natsmap/README.md) —
  `RegisterBatchedHandler[T]` wires the batched-dispatch pattern
  onto a JetStream Pull subscriber. YAML configures `batch_size`
  and `batch_interval`; the kit handles Submit + ack/nak atomically.
- `examples/urlshort/internal/links/visit_counter.go` — production
  use case via the natsmap path: one slice of events per batch →
  domain-side aggregation → one UPDATE … FROM (VALUES …).
