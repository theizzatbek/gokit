// Package wsnats bridges browser WebSocket clients to NATS pub/sub.
// Per-connection subscribes flow inbound NATS messages out to the WS
// client; per-frame publishes flow outbound WS frames to a NATS
// subject.
//
// # Concurrency model
//
// The kit owns the WS connection's IO lifecycle end-to-end. Callers
// supplying [BridgeFn], [Bridge.OnMessage], or [Bridge.OnFrame] must
// NOT spawn their own goroutines that read or write on the
// *websocket.Conn — both directions are kit-managed and the contract
// is exclusive:
//
//   - One main goroutine reads frames from the WS conn (the loop
//     started inside the handler). No other goroutine — kit-side or
//     caller-side — may call ws.ReadMessage; a second reader produces
//     undefined behaviour from gorilla/fasthttp websocket.
//   - Writes can fire from arbitrary NATS subscription handlers; the
//     kit serialises them through an internal mutex so the underlying
//     conn (which is NOT goroutine-safe for writes) stays consistent.
//     Callbacks should return their payload via [Bridge.OnMessage] /
//     [Bridge.OnFrame] — never call ws.WriteMessage directly.
//   - On any exit signal (ctx done, WS read error, callback error)
//     the kit cancels its per-connection context, forces an immediate
//     ws.ReadDeadline so the main loop's blocking read returns
//     promptly, unsubscribes every NATS subscription, and closes the
//     conn. Callers that need post-close logic should chain it via
//     fibermap middleware (or [fibermap.Engine] hooks) — NOT a
//     goroutine spawned from the bridge.
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
