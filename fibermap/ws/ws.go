package ws

import (
	"context"
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"

	"github.com/theizzatbek/gokit/fibermap"
)

// Stable error Code constants returned by the kit's WebSocket layer.
const (
	// CodeUpgradeRequired — the route fired but the request did not
	// carry a valid WebSocket Upgrade header. Surfaces as HTTP 426
	// (fiber.ErrUpgradeRequired) so plain HTTP clients see a stable
	// status code without dragging the gofiber/websocket dep into
	// their error-handling path.
	CodeUpgradeRequired = "ws_upgrade_required"
)

// HandlerFunc is the per-connection WebSocket callback. fibermap
// invokes it once per established connection with:
//
//   - `ctx`  — the request's UserContext at upgrade time; useful for
//     downstream timeouts the kit-side caller may have attached
//     (request_timeout middleware, etc.). The kit does NOT cancel
//     this ctx when the WebSocket closes — that signal lives in the
//     `conn.ReadMessage()` error return.
//   - `c`    — the kit's typed *Context[T] frozen at upgrade time. The
//     Data field is populated by the engine's ContextBuilder before
//     the handler runs (so auth claims, request-id, tenant hints are
//     all visible inside the WS callback).
//   - `conn` — the upstream gofiber/websocket Conn. Use ReadMessage /
//     WriteMessage / ReadJSON / WriteJSON / Close directly.
//
// The kit auto-Closes the connection when the handler returns. A
// non-nil error from the handler is logged via fibermap's reqlogger
// when wired; clients see the same close-frame regardless.
type HandlerFunc[T any] func(ctx context.Context, c *fibermap.Context[T], conn *websocket.Conn) error

// Register binds a WebSocket handler under `name` in the engine's
// handler map. The actual fibermap handler the engine sees is a
// wrapper that:
//
//  1. Rejects non-upgrade requests up front with
//     *errs.Error{Kind: KindBadRequest, Code: "ws_upgrade_required"}
//     — fibermap.ErrorHandler renders this as HTTP 426.
//  2. Captures the kit's Context[T] (Data, UserContext) via closure
//     so the websocket callback can read them after the upgrade.
//  3. Hands off to websocket.New for the actual handshake +
//     callback driving.
//
// The route's middleware chain runs BEFORE the kit-side upgrade
// check (so auth / rate-limit / etc. can reject without ever
// upgrading the connection — clients see the middleware's error
// instead of a WebSocket close-frame).
//
// Panics with fibermap's stable Codes on duplicate registration
// (same as [fibermap.Engine.RegisterHandler]).
func Register[T any](eng *fibermap.Engine[T], name string, fn HandlerFunc[T], cfgOpts ...websocket.Config) {
	if eng == nil {
		panic(errors.New("ws.Register: engine is nil"))
	}
	if fn == nil {
		panic(errors.New("ws.Register: handler is nil"))
	}
	if len(cfgOpts) > 1 {
		panic(errors.New("ws.Register: at most one websocket.Config allowed"))
	}
	eng.RegisterHandler(name, func(c *fibermap.Context[T]) error {
		if !websocket.IsWebSocketUpgrade(c.Ctx) {
			return fiber.ErrUpgradeRequired
		}
		ctx := c.UserContext()
		var cfg websocket.Config
		if len(cfgOpts) == 1 {
			cfg = cfgOpts[0]
		}
		return websocket.New(func(conn *websocket.Conn) {
			defer func() { _ = conn.Close() }()
			_ = fn(ctx, c, conn)
		}, cfg)(c.Ctx)
	})
}
