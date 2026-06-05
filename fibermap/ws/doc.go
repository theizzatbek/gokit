// Package ws adds WebSocket handlers to fibermap on top of
// github.com/gofiber/websocket/v2. The subpackage lives outside core
// fibermap so callers that do not need WebSockets do not transitively
// pull the upstream websocket + fasthttp/websocket deps.
//
// Quickstart:
//
//	import (
//	    "github.com/gofiber/websocket/v2"
//	    "github.com/theizzatbek/gokit/fibermap"
//	    fibermapws "github.com/theizzatbek/gokit/fibermap/ws"
//	)
//
//	eng := fibermap.New[appCtx]()
//	eng.SetContextBuilder(buildAppCtx)
//
//	fibermapws.Register(eng, "chat.connect",
//	    func(ctx context.Context, c *fibermap.Context[appCtx], conn *websocket.Conn) error {
//	        for {
//	            _, msg, err := conn.ReadMessage()
//	            if err != nil { return err }
//	            if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
//	                return err
//	            }
//	        }
//	    })
//
// And in routes.yaml:
//
//   - method: GET
//     path:   /ws/chat
//     handler: chat.connect
//     middleware:
//   - auth: required   # runs BEFORE the upgrade handshake — non-WS clients get a regular 401
//
// The kit checks IsWebSocketUpgrade up front (non-upgrade requests
// get HTTP 426 with stable code "ws_upgrade_required") and then
// installs the websocket.New upgrade. Middleware in the YAML chain
// runs BEFORE the upgrade — auth, rate-limit, etc. behave as usual.
package ws
