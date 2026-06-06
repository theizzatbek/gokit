# fibermap extras

Дополнения поверх ядра `fibermap/` (см. архитектурный раздел в `CLAUDE.md`):
`ErrorHandler`, SSE, WebSocket, WebSocket↔NATS bridge.

## `fibermap.ErrorHandler`

`fibermap.ErrorHandler(logger *slog.Logger) fiber.ErrorHandler` (in `fibermap/error_handler.go`) wires `errs.HTTP` into Fiber and falls back to `*fiber.Error`'s own code for router-level errors (404/405). Auto-logs 5xx responses via the passed logger; 4xx is silent by default. Pass `nil` logger to use `slog.Default()`.

## `fibermap/sse/`

Server-Sent Events handlers for fibermap. `sse.Register[T](eng, name, fn)` registers a YAML-resolvable handler whose wrapped fiber.Handler emits SSE headers (`Content-Type: text/event-stream`, `Cache-Control: no-cache, no-store`, `Connection: keep-alive`, `X-Accel-Buffering: no`) and installs `fasthttp.SetBodyStreamWriter` pointing at the kit's `*Stream` wrapper.

`HandlerFunc[T] = func(ctx context.Context, c *fibermap.Context[T], s *sse.Stream) error`.

Stream surface: `Send(event, data string) error` (auto-flush; multi-line `data` is split into per-line `data:` fields per SSE spec; empty event-name suppresses the `event:` line), `SendJSON(event string, payload any) error` (json.Marshal + Send), `Comment(text string) error` for keep-alive pings, `Err() error` returns the first write/flush failure for handlers that want to detect disconnect without checking every Send return.

Stream NOT safe for concurrent use — pin one goroutine per Stream; for multi-publisher use a channel + single fan-in goroutine. Disconnect surfaces ONLY via Send-return / Stream.Err — kit does not cancel ctx on client disconnect.

YAML routes wire normally (`handler: events.stream` in routes.yaml); middleware chain runs BEFORE the SSE wrap so auth/rate-limit reject with normal HTTP responses. Dep-free beyond fiber + stdlib.

## `fibermap/ws/`

WebSocket handlers for fibermap on top of `gofiber/websocket/v2` (kept in a subpackage so callers that do not need WebSockets do not transitively pull the upstream websocket + fasthttp/websocket deps).

`ws.Register[T](eng, name, fn, cfgOpts...)` registers a YAML-resolvable handler whose wrapped fiber.Handler: (1) checks `websocket.IsWebSocketUpgrade` upfront — non-upgrade GET returns `fiber.ErrUpgradeRequired` (HTTP 426) with stable code `ws_upgrade_required`; (2) hands off to `websocket.New` which performs the handshake + drives the per-connection callback; (3) auto-closes the conn when the handler returns.

`HandlerFunc[T] = func(ctx context.Context, c *fibermap.Context[T], conn *websocket.Conn) error` — ctx is request's UserContext frozen at upgrade time (kit does NOT cancel it on close; disconnect surfaces via `conn.ReadMessage` error), `c.Data` is populated by the engine's `ContextBuilder` BEFORE the handler runs so auth claims / request-id / tenant hints are visible inside the WS callback.

Optional trailing `websocket.Config` (at most one — panics on multiple) tunes ReadBufferSize / WriteBufferSize / EnableCompression / etc. Middleware chain on the route runs BEFORE the upgrade check — auth, rate-limit, etc. reject with normal HTTP responses so plain HTTP clients see middleware errors instead of WebSocket close-frames.

Use for direct browser ↔ server custom protocol with per-connection state owned by a single backend instance; reach for `fibermap/wsnats` instead when fan-out across backend instances OR NATS-native subject routing is what you actually want.

## `fibermap/wsnats/`

Bridge browser WebSocket clients ↔ NATS pub/sub. Sits on top of `fibermap/ws` (same upgrade machinery) and `clients/nats` (for the underlying `*nats.Conn` via `natsclient.Client.Conn()`).

`wsnats.Register[T](eng, name, nc, fn, cfgOpts...)` takes a `BridgeFn[T] = func(ctx, c) (Bridge, error)` invoked once per successful upgrade; `Bridge{Subscribe []string, Publish string, QueueGroup string, Binary bool, OnMessage, OnFrame}` configures the routing: Subscribe subjects flow inbound NATS messages → WS client, Publish forwards each WS frame to a NATS subject (empty = read-only stream), QueueGroup load-balances Subscribe across backend instances, Binary toggles BinaryMessage frames (affects both directions), OnMessage/OnFrame are optional transforms that can drop messages by returning `(nil, nil)`.

Kit serializes WS writes via a mutex (multiple NATS subscriptions can fire concurrently but gofiber/fasthttp Conn writes are NOT goroutine-safe), defers unsubscribe of every NATS subscription on cleanup, and uses a per-connection cancel ctx so late-arriving NATS messages on a closed WS do not race against the unsubscribe loop.

**When to reach for it over `fibermap/ws`:** multi-server fan-out (every backend instance auto-subscribes once per WS conn; NATS routes one publish to every instance and the kit forwards to every connected client without manual fan-out wiring), notifications/live-dashboards/multi-tenant patterns where per-tenant NATS subjects + ACLs handle authorization, and chat rooms where state lives in NATS subjects rather than backend memory. Stay on `fibermap/ws` for custom binary protocols, game-server tick loops, or single-instance chat-room cases where NATS is unnecessary overhead. Panics on nil NATS client / nil BridgeFn / underlying `*nats.Conn` missing.