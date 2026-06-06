# fibermap/ws

WebSocket handlers для `fibermap` поверх `github.com/gofiber/websocket/v2`. Subпакет живёт за пределами core fibermap, чтобы callers, не использующие WebSockets, не тащили upstream websocket + fasthttp/websocket deps.

**Импорт:** `github.com/theizzatbek/gokit/fibermap/ws`
**Зависит от:** `fibermap`, `gofiber/websocket/v2`

## Quickstart

```go
import (
    "context"
    "github.com/gofiber/websocket/v2"
    "github.com/theizzatbek/gokit/fibermap"
    fibermapws "github.com/theizzatbek/gokit/fibermap/ws"
)

type appCtx struct{ Subject string }

eng := fibermap.New[appCtx]()
eng.SetContextBuilder(buildAppCtx)

fibermapws.Register(eng, "chat.connect",
    func(ctx context.Context, c *fibermap.Context[appCtx], conn *websocket.Conn) error {
        for {
            _, msg, err := conn.ReadMessage()
            if err != nil { return err }
            if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
                return err
            }
        }
    })
```

И в `routes.yaml`:

```yaml
- method: GET
  path:   /ws/chat
  handler: chat.connect
  middleware:
    - auth: required   # runs ПЕРЕД upgrade'ом — non-WS clients get обычный 401
```

## Что делает Register

1. Регистрирует под `name` обычный fibermap handler.
2. Wrapped handler:
   - Проверяет `websocket.IsWebSocketUpgrade(c)`; non-upgrade GET → `fiber.ErrUpgradeRequired` (HTTP 426) с stable code `ws_upgrade_required`.
   - Хватает kit's `*Context[T]` (Data, UserContext) через closure — websocket callback читает их после handshake'а.
   - Hand'ит control к `websocket.New` для actual upgrade + callback driving.
3. Auto-closes connection после возврата handler'а.

Middleware chain маршрута runs BEFORE upgrade check — auth / rate-limit / etc. могут отвергнуть запрос обычным HTTP-error response'ом до того, как connection upgrade'нется.

## HandlerFunc

```go
type HandlerFunc[T any] func(
    ctx context.Context,
    c *fibermap.Context[T],
    conn *websocket.Conn,
) error
```

- `ctx` — request's UserContext frozen at upgrade time. Kit НЕ cancel'ит этот ctx когда WebSocket закрывается; disconnect signal — `conn.ReadMessage()` returning error.
- `c` — kit's typed `*Context[T]` frozen at upgrade time. `c.Data` populated by engine's `ContextBuilder` BEFORE handler runs — auth claims, request-id, tenant hints visible inside WS callback.
- `conn` — upstream `gofiber/websocket.Conn`. Use ReadMessage / WriteMessage / ReadJSON / WriteJSON / Close directly.

Non-nil return logged via fibermap's reqlogger когда подключён; client видит тот же close-frame regardless.

## Опциональная websocket.Config

```go
fibermapws.Register(eng, "ws.large", handler,
    websocket.Config{
        ReadBufferSize:  4096,
        WriteBufferSize: 4096,
        EnableCompression: true,
    })
```

Передавайте at most one config — kit panics on multiple. Без config'а kit uses upstream defaults (`gofiber/websocket` v2's).

## Error-Code'ы

| Code | Когда | Заметки |
|---|---|---|
| `ws_upgrade_required` | Plain GET к WS-route'у без `Upgrade: websocket` header'ом | Маппит в HTTP 426 через `fiber.ErrUpgradeRequired` |

## Authentication patterns

Middleware на маршруте runs **до** upgrade'а. Auth claims хранятся в `c.Data` через `ContextBuilder` — внутри WS callback читай их напрямую:

```go
eng.SetContextBuilder(func(c *fiber.Ctx) (appCtx, error) {
    return appCtx{Subject: c.Locals("user_id").(string)}, nil
})

fibermapws.Register(eng, "chat.connect",
    func(_ context.Context, c *fibermap.Context[appCtx], conn *websocket.Conn) error {
        log.Printf("WS connection from %s", c.Data.Subject)
        // …
    })
```

Если auth-middleware reject'ит request (return non-nil), upgrade не происходит — клиент получает middleware's error как обычный HTTP response.

## Когда `fibermap/ws` vs `fibermap/wsnats`

- **`fibermap/ws`** (этот пакет) — direct browser ↔ server custom protocol. Каждое соединение owns its own state on a single backend instance. Подходит для chat-room'ов на одном process'е, game-server'ов с tick-loop'ом per-conn, custom binary protocol'ов.
- **`fibermap/wsnats`** ([README](../wsnats/README.md)) — bridge browser WebSocket'а к NATS pub/sub. Каждое соединение subscribes к NATS subject'ам; messages фан-аутся ко всем connected client'ам across all backend instances автоматически. Подходит для live dashboards, notifications, multi-tenant fan-out, chat rooms где state живёт в NATS subjects а не в backend memory.

См. [`fibermap/wsnats/README.md`](../wsnats/README.md) для full comparison table.

## См. также

- [`fibermap`](../README.md) — родительский router
- [`fibermap/wsnats`](../wsnats/README.md) — WebSocket с NATS pub/sub bridge'ем
- [`fibermap/sse`](../sse/README.md) — SSE equivalent (dep-free, только fiber + stdlib)
- [gofiber/websocket](https://github.com/gofiber/websocket) — upstream upgrade library
