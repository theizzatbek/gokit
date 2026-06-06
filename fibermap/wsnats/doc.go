// Package wsnats bridges browser WebSocket clients to NATS pub/sub.
// Per-connection subscribes flow inbound NATS messages out to the WS
// client; per-frame publishes flow outbound WS frames to a NATS
// subject. The kit handles concurrency (writes to the WS conn are
// serialised so multiple subscriptions can fire safely) and cleanup
// (subscriptions unsubscribe when the WS closes).
//
// The subpackage sits on top of [fibermap/ws] (the upgrade /
// handshake machinery is identical) and [clients/nats] (for the
// underlying *nats.Conn). When you do NOT need NATS in the loop —
// custom protocol per connection, chat-room state owned by the
// process — reach for fibermap/ws directly and skip this layer.
//
// Quickstart:
//
//	wsnats.Register(eng, "notifications.connect", svc.NATS,
//	    func(ctx context.Context, c *fibermap.Context[appCtx]) (wsnats.Bridge, error) {
//	        return wsnats.Bridge{
//	            Subscribe: []string{"notifications." + c.Data.UserID + ".*"},
//	            Publish:   "notifications." + c.Data.UserID + ".ack",
//	        }, nil
//	    })
//
// Routes.yaml entry is identical to the plain fibermap/ws case:
//
//   - method: GET
//     path: /ws/notifications
//     handler: notifications.connect
//     middleware:
//   - auth: required
//
// See README.md for the full comparison with the in-process
// fibermap/ws subpackage.
package wsnats
