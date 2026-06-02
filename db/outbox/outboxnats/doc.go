// Package outboxnats wires [db/outbox] onto [clients/natsmap]'s
// [natsmap.PublishRaw] — turning the manual three-line closure
//
//	w, _ := outbox.NewWorker(db, func(ctx context.Context, e outbox.Event) error {
//	    return natsmap.PublishRaw(ctx, rt, e.EventType, e.Payload, e.Headers)
//	})
//
// into a single call:
//
//	w, _ := outbox.NewWorker(db, outboxnats.NewPublisher(rt))
//
// The mapping is intentionally trivial — the adapter does not encode,
// decode, or transform anything. EventType maps onto a registered
// natsmap publisher name (default 1:1; override with
// [WithPublisherNameFn] when YAML publisher names differ from the
// EventType strings, e.g. when the bus subject is namespaced).
//
// The package lives at db/outbox/outboxnats so [db/outbox] stays free
// of any [clients/natsmap] import — symmetric to [auth/refreshpg]
// bridging [auth] and [db].
package outboxnats
