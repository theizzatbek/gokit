// Package inbox is the consumer-side companion to [db/outbox]. It
// implements the inbox table pattern that converts at-least-once
// pub/sub delivery into effectively-once processing.
//
// The kit ships:
//
//   - The DDL ([Schema]) — composite PK `(consumer, event_id)` so
//     fan-out consumers share a table without colliding.
//   - [Process] / [Inbox.Process] — runs the caller's domain fn under
//     a single transaction; if the (consumer, event_id) row already
//     exists, fn is NOT called and [OutcomeDuplicate] is returned.
//   - [RetentionWorker] — periodic DELETE of rows older than a
//     caller-supplied TTL.
//
// Typical use:
//
//	if _, err := svc.DB.Exec(ctx, inbox.Schema()); err != nil { return err }
//
//	outcome, err := inbox.Process(ctx, svc.DB, inbox.Key{
//	    Consumer: "orders-svc:link.created",
//	    EventID:  msg.Headers.Get("Nats-Msg-Id"),
//	}, func(tx *db.Tx) error {
//	    return persistOrder(ctx, tx, evt)
//	})
//	if err != nil { return err }
//	// OutcomeDuplicate is success — just ack the redelivery.
//
// Effectively-once boundary:
//
//   - DB-side state is exactly-once (UNIQUE on (consumer, event_id)
//   - same-Tx commit).
//   - Side effects OUTSIDE the DB (third-party HTTP, file writes)
//     are NOT guaranteed by inbox alone — layer your own idempotency
//     keys for those.
//
// See also [db/outbox] (the publish-side mirror) and
// [db/inbox/inboxnats] (the natsmap handler wrapper).
package inbox
