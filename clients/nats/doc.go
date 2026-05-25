// Package natsclient is the kit's NATS / JetStream wrapper. It offers a typed,
// JetStream-first publish/subscribe surface with generic Publisher[T] /
// Subscribe[T], push-based delivery with MaxInFlight backpressure, auto-ack
// handlers (nil → Ack, err → Nak with backoff, decode-fail → Term),
// idempotent EnsureStream, and opt-in slog/Prometheus observability.
//
// Typical wiring:
//
//	c, err := natsclient.Connect(ctx, natsclient.Config{
//	    URL: "nats://localhost:4222", Name: "myapp",
//	}, natsclient.WithLogger(logger))
//	if err != nil { return err }
//	defer c.Close()
//
//	err = c.EnsureStream(ctx, natsclient.StreamConfig{
//	    Name: "ORDERS", Subjects: []string{"orders.>"},
//	    MaxAge: 7 * 24 * time.Hour,
//	})
//
//	pub := natsclient.NewPublisher[OrderCreated](c)
//	pub.Publish(ctx, "orders.created", OrderCreated{ID: "..."})
//
//	sub, _ := natsclient.Subscribe[OrderCreated](ctx, c, "orders.created",
//	    func(ctx context.Context, m natsclient.Msg[OrderCreated]) error {
//	        return process(ctx, m.Data)
//	    },
//	    natsclient.WithDurable("processor"),
//	    natsclient.WithMaxInFlight(16),
//	)
//	defer sub.Drain()
//
// Errors returned by every method are *errs.Error
// (see github.com/theizzatbek/fibermap/errs).
//
// See docs/superpowers/specs/2026-05-25-kit-nats-design.md for the full design.
package natsclient
