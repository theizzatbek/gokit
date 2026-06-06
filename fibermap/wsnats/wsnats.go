package wsnats

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gofiber/websocket/v2"
	"github.com/nats-io/nats.go"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/fibermap"
	fibermapws "github.com/theizzatbek/gokit/fibermap/ws"
)

// Bridge is the per-connection routing config built once by the
// caller's [BridgeFn] when a WS handshake completes. Zero-value
// Bridge is legal — produces a no-op connection that immediately
// closes (kit-defined "nothing to bridge → exit cleanly").
type Bridge struct {
	// Subscribe lists NATS subjects whose messages are forwarded to
	// the WS client. Each subject opens one core-NATS subscription;
	// every received Msg is sent as a TextMessage frame (BinaryMessage
	// when Binary == true).
	//
	// Subjects support the standard NATS wildcards (`>` rest-of-
	// subject, `*` one token). For high-fanout topics consider
	// QueueGroup to load-balance across backend instances instead of
	// every instance receiving every message.
	Subscribe []string

	// Publish, when non-empty, is the NATS subject every WS frame
	// the client sends gets published to. Empty = no inbound publish
	// (read-only stream from server to browser).
	Publish string

	// QueueGroup, when non-empty, joins all Subscribe-subjects under
	// this NATS queue group — only one subscriber in the group
	// receives each message. Use for load-balanced fan-out where
	// "any backend handles this notification" is enough; leave empty
	// for true broadcast (every connected client on every backend
	// sees every message).
	QueueGroup string

	// Binary toggles BinaryMessage frames instead of TextMessage.
	// Affects BOTH directions; mixing text + binary in one bridge is
	// not supported (would force per-message metadata the kit refuses
	// to invent).
	Binary bool

	// OnMessage, when non-nil, transforms a NATS message before it
	// flows to the WS client. Return `(nil, nil)` to drop the message
	// silently; a non-nil error closes the connection with the error
	// as the close reason.
	//
	// Default (nil): forward msg.Data verbatim.
	OnMessage func(msg *nats.Msg) ([]byte, error)

	// OnFrame, when non-nil, transforms a WS-received frame before it
	// is published to the Publish subject. Return `(nil, nil)` to
	// skip the publish without an error; a non-nil error closes the
	// connection.
	//
	// Default (nil): forward payload verbatim.
	OnFrame func(payload []byte) ([]byte, error)
}

// BridgeFn is the per-connection config builder. Called ONCE
// immediately after a successful WebSocket upgrade and BEFORE the
// kit subscribes to any NATS subject. Use for context-aware routing
// — e.g. derive Subscribe subjects from the authenticated user id
// available via c.Data.
//
// Returning a non-nil error closes the WS connection cleanly without
// surfacing the error to the client (the error is logged by fibermap's
// reqlogger when wired).
type BridgeFn[T any] func(ctx context.Context, c *fibermap.Context[T]) (Bridge, error)

// Register binds a NATS-bridged WebSocket handler under `name`. The
// kit wraps the call as a regular fibermap/ws handler — middleware
// chain on the YAML route runs BEFORE the upgrade so auth / rate-
// limit / etc. reject plain HTTP clients with normal responses.
//
// Lifecycle of one accepted connection:
//
//  1. Upgrade completes (fibermap/ws machinery).
//  2. BridgeFn runs; returned Bridge configures the loop.
//  3. One NATS subscription per Bridge.Subscribe subject; messages
//     fan in to a single goroutine that owns the WS writer (locks
//     prevent concurrent writes — gorilla/fasthttp websocket conns
//     are NOT write-safe across goroutines).
//  4. The main goroutine reads WS frames; if Bridge.Publish != "",
//     each frame is forwarded to NATS.
//  5. On any error (WS read fail, NATS publish fail, OnMessage
//     error, ctx done) the loop bails out, every subscription is
//     unsubscribed, and the connection closes.
//
// Panics with fibermap's stable Codes on nil engine / nil handler /
// nil NATS client.
func Register[T any](
	eng *fibermap.Engine[T],
	name string,
	nc *natsclient.Client,
	fn BridgeFn[T],
	cfgOpts ...websocket.Config,
) {
	if nc == nil {
		panic(errors.New("wsnats.Register: NATS client is nil"))
	}
	if fn == nil {
		panic(errors.New("wsnats.Register: BridgeFn is nil"))
	}
	conn := nc.Conn()
	if conn == nil {
		panic(errors.New("wsnats.Register: NATS client has no underlying *nats.Conn"))
	}
	fibermapws.Register(eng, name, func(
		ctx context.Context,
		c *fibermap.Context[T],
		ws *websocket.Conn,
	) error {
		bridge, err := fn(ctx, c)
		if err != nil {
			return err
		}
		return runBridge(ctx, ws, conn, bridge)
	}, cfgOpts...)
}

// runBridge is the per-connection loop. Split out so the
// hot path stays mockable (tests can call it with a stub).
func runBridge(
	ctx context.Context,
	ws *websocket.Conn,
	nc *nats.Conn,
	b Bridge,
) error {
	msgType := websocket.TextMessage
	if b.Binary {
		msgType = websocket.BinaryMessage
	}

	// Serialise WS writes — multiple NATS subscriptions can fire
	// concurrently, but gofiber/websocket Conn writes are NOT safe
	// for concurrent use.
	var writeMu sync.Mutex
	write := func(payload []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return ws.WriteMessage(msgType, payload)
	}

	// Per-connection cancel so subscription handlers know when the
	// main loop has bailed out — without this, an in-flight NATS msg
	// callback might race against the unsubscribe loop on close.
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Reader-unblocker: ws.ReadMessage blocks until the next frame
	// from the client. When loopCtx cancels (subscription callback
	// errored, parent ctx done) we want the main goroutine to exit
	// promptly even if the client is silent. Setting a past
	// ReadDeadline forces an immediate timeout error on the
	// in-flight read, which the main loop bubbles up cleanly. Wired
	// once via a goroutine that exits the moment loopCtx fires —
	// the cleanup chain (cancel → reader returns → subs unsubscribe
	// → ws closes) becomes deterministic instead of waiting on
	// client traffic.
	readerDone := make(chan struct{})
	go func() {
		select {
		case <-loopCtx.Done():
			_ = ws.SetReadDeadline(time.Now())
		case <-readerDone:
		}
	}()
	defer close(readerDone)

	subs := make([]*nats.Subscription, 0, len(b.Subscribe))
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	for _, subject := range b.Subscribe {
		handler := func(msg *nats.Msg) {
			if loopCtx.Err() != nil {
				return
			}
			payload := msg.Data
			if b.OnMessage != nil {
				out, err := b.OnMessage(msg)
				if err != nil {
					cancel()
					return
				}
				if out == nil {
					return // dropped
				}
				payload = out
			}
			if err := write(payload); err != nil {
				cancel()
				return
			}
		}
		var (
			sub *nats.Subscription
			err error
		)
		if b.QueueGroup != "" {
			sub, err = nc.QueueSubscribe(subject, b.QueueGroup, handler)
		} else {
			sub, err = nc.Subscribe(subject, handler)
		}
		if err != nil {
			return err
		}
		subs = append(subs, sub)
	}

	// Inbound loop: read WS frames, forward to NATS via Publish.
	// Bails out on read error (client disconnect), ctx done, or
	// publish error.
	for {
		if err := loopCtx.Err(); err != nil {
			return nil
		}
		_, payload, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		if b.Publish == "" {
			continue
		}
		out := payload
		if b.OnFrame != nil {
			transformed, err := b.OnFrame(payload)
			if err != nil {
				return err
			}
			if transformed == nil {
				continue // skip
			}
			out = transformed
		}
		if err := nc.Publish(b.Publish, out); err != nil {
			return err
		}
	}
}
