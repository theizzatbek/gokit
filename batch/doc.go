// Package batch is the kit's generic batched-handler dispatcher.
// It collects typed items via Submit and hands them off to the
// caller's HandlerFn as a slice — one call per buffered batch.
//
// Per-item ack callbacks let the kit's upstream sources (e.g. the
// natsmap pull subscriber) commit the whole batch atomically: the
// handler's single return value flows into every item's ack closure,
// so all items in a batch are Ack'd together (on nil) or Nak'd
// together (on err).
//
//	b, err := batch.New[Event](batch.Config[Event]{
//	    HandlerFn: func(ctx context.Context, batch []Event) error {
//	        return persistAll(ctx, batch) // one transaction
//	    },
//	    BatchSize: 1000,        // required
//	    Interval:  time.Second, // default 1s when zero
//	    Logger:    logger,
//	})
//	if err != nil { return err }
//	defer b.Close()
//
//	// On each incoming message:
//	b.Submit(event, func(err error) {
//	    if err == nil {
//	        msg.Ack()
//	    } else {
//	        msg.Nak()
//	    }
//	})
//
// The package is event-source agnostic; the kit's natsmap layer
// wires it onto a JetStream Pull subscriber under the hood when a
// subscriber's YAML declares batch_size.
//
// Delivery semantics:
//
//   - At the dispatch boundary the batcher is at-most-once with
//     respect to the upstream source — items live in memory between
//     Submit and the next flush; a crash mid-buffer drops them.
//   - The ack-callback channel turns that into at-least-once when
//     the caller defers the upstream commit (Ack) until ack(nil)
//     fires. The natsmap integration uses exactly this pattern.
package batch
