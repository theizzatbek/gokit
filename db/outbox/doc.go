// Package outbox implements the transactional-outbox pattern over
// Postgres: events are written to an `outbox` table inside the same
// transaction as the business state, then a background Worker dispatches
// them to the real bus (NATS / Kafka / SQS) with at-least-once semantics.
//
// The point is to close the crash window between a committed DB write
// and a published event. Without an outbox, a service that does
// `tx.Commit()` then `nats.Publish(...)` can crash between the two —
// the row is persisted, the event isn't, downstream consumers miss it.
// With an outbox, both writes share the same transaction; the publish
// becomes a separate, retryable concern owned by the Worker.
//
// # Lifecycle
//
//  1. Apply schema.sql to the target database (the kit ships the DDL;
//     migration runner is out of scope).
//  2. Inside a `db.DB.Tx` block, call [Enqueue] alongside your business
//     writes. The event lands in the outbox table iff the transaction
//     commits.
//  3. A long-lived [Worker] polls the outbox table on a configurable
//     interval, selects unpublished rows with `FOR UPDATE SKIP LOCKED`
//     (multi-replica safe), invokes the caller-supplied PublishFn for
//     each event, and marks them published on success — or bumps
//     `attempts` and records the error message on failure.
//
// # Semantics
//
//   - Transactional consistency: an event is only persisted if the
//     surrounding business transaction commits.
//   - At-least-once delivery: a crash AFTER PublishFn succeeds but
//     BEFORE the row's UPDATE will redeliver the event next tick.
//     Use JetStream's `Nats-Msg-Id` header (the event ID) to dedupe
//     at the consumer.
//   - Multi-replica safe: every Worker selects with SKIP LOCKED so two
//     instances draining the same table don't contend.
//   - Bounded backoff on failure: PublishFn errors bump `attempts` and
//     stash `last_error`. The row stays unpublished and gets retried
//     next tick. Cap with [WithMaxAttempts] to dead-letter after N
//     failures.
//
// # Sketch
//
//	err := svc.DB.Tx(ctx, func(tx *db.Tx) error {
//	    if _, err := svc.LinksRepo.Create(ctx, tx, link); err != nil {
//	        return err
//	    }
//	    return outbox.Enqueue(ctx, tx, outbox.Event{
//	        AggregateType: "link",
//	        AggregateID:   link.Code,
//	        EventType:     "urlshort.link.created",
//	        Payload:       mustJSON(link),
//	    })
//	})
//
//	w, _ := outbox.NewWorker(svc.DB, func(ctx context.Context, e outbox.Event) error {
//	    return natsmap.PublishRaw(ctx, svc.NATSMap, e.EventType, e.Payload, e.Headers)
//	})
//	w.Start(ctx)
//	svc.OnShutdown(w.Stop)
package outbox
