# fibermap/wsnats

Bridge browser WebSocket clients ↔ NATS pub/sub. Per-connection NATS-subscriptions flow inbound messages out к WS client; per-frame publishes flow outbound WS frames в NATS subject.

**Импорт:** `github.com/theizzatbek/gokit/fibermap/wsnats`
**Зависит от:** `fibermap`, `fibermap/ws`, `clients/nats`, `gofiber/websocket/v2`, `nats-io/nats.go`

## Quickstart

```go
import (
    "context"
    "github.com/theizzatbek/gokit/fibermap"
    "github.com/theizzatbek/gokit/fibermap/wsnats"
)

type appCtx struct{ UserID string }

eng := fibermap.New[appCtx]()
eng.SetContextBuilder(buildAppCtx)

wsnats.Register(eng, "notifications.connect", svc.NATS,
    func(ctx context.Context, c *fibermap.Context[appCtx]) (wsnats.Bridge, error) {
        return wsnats.Bridge{
            Subscribe: []string{"notifications." + c.Data.UserID + ".*"},
            Publish:   "notifications." + c.Data.UserID + ".ack",
        }, nil
    })
```

И в `routes.yaml`:

```yaml
- method: GET
  path:   /ws/notifications
  handler: notifications.connect
  middleware:
    - auth: required   # выставляет c.Data.UserID до upgrade'а
```

## Bridge API

```go
type Bridge struct {
    Subscribe  []string  // NATS subjects → WS client
    Publish    string    // WS frames → NATS subject (пусто = read-only)
    QueueGroup string    // optional: load-balance Subscribe through queue group
    Binary     bool      // BinaryMessage frames вместо TextMessage
    OnMessage  func(msg *nats.Msg) ([]byte, error)  // transform NATS → WS; nil,nil = drop
    OnFrame    func(payload []byte) ([]byte, error) // transform WS → NATS; nil,nil = skip
}

type BridgeFn[T any] func(ctx context.Context, c *fibermap.Context[T]) (Bridge, error)

func Register[T any](
    eng *fibermap.Engine[T],
    name string,
    nc *natsclient.Client,
    fn BridgeFn[T],
    cfgOpts ...websocket.Config, // optional, at most one
)
```

`BridgeFn` вызывается ОДИН раз сразу после успешного WS-upgrade'а. Возврат non-nil error закрывает соединение cleanly (без surface'а ошибки клиенту); логируется через fibermap's reqlogger когда подключён.

## Lifecycle одного соединения

1. **Upgrade**: fibermap/ws machinery completes handshake.
2. **BridgeFn**: caller's closure runs → returns `Bridge` config.
3. **Subscribe**: kit открывает ОДНУ core-NATS subscription на каждый `Bridge.Subscribe` subject. Handler'ы fan-in в kit-managed writer (mutex serializes — gorilla/fasthttp WS conn'ы НЕ write-safe across goroutines).
4. **Read loop**: main goroutine читает WS frames; на каждый frame, если `Bridge.Publish != ""`, kit forwards в NATS.
5. **Cleanup**: на любую ошибку (WS read fail, NATS publish fail, OnMessage error, ctx done) loop bail'ит out, **все subscriptions unsubscribe'аются**, connection closes.

## Когда использовать `wsnats` vs `fibermap/ws`

| Аспект | `fibermap/ws` (PR #144) | `fibermap/wsnats` |
|---|---|---|
| Use case | Direct browser ↔ server custom protocol | Browser ↔ NATS pub/sub bridge |
| Stateful loop | Yes — handler владеет каждым соединением | Mostly stateless — NATS subjects = state |
| Multi-server fan-out | **Нет** — каждый backend instance изолирован | **Да** — любой NATS-published message landит на все connected clients across all instances |
| Сложность setup'а | Зависит только от `gofiber/websocket/v2` | Требует NATS-сервер running |
| ACL | Только middleware ДО upgrade'а | + per-subject ACL внутри NATS (Subjects + permissions) |
| Persistence | Нет; restart wipes state | NATS JetStream поверх если нужно durable streams |

### Когда reach for `fibermap/ws`

- **Chat-room** где state owned by единственным backend process'ом.
- **Game-server** с tick-based simulation per-connection.
- **Custom binary protocol** (e.g., proto over WS) которым NATS pub/sub overhead is excess.
- Single-instance deployment где fan-out не нужен.

### Когда reach for `fibermap/wsnats`

- **Live dashboards** где the same updates feed N user'ов on N backend instances.
- **Notifications** — сервер publishes в `notifications.{user}.*`, browser получает via WS.
- **Chat rooms across backends** — room state stored в NATS subjects, любой backend serves clients.
- **Multi-tenant fan-out** где per-tenant subscriptions handle ACLs server-side.

### Comparison: fan-out behaviour

Допустим 3 backend instances, 100 connected WS clients each (300 total), сервис publishes `event` в NATS:

| Pattern | What happens |
|---|---|
| `fibermap/ws` + manual NATS pubsub в handler'е | Each instance must subscribe + fan-out manually — duplicate code, bugs around concurrency, leaks on disconnect |
| `fibermap/wsnats` | Каждый instance auto-subscribes once per WS conn; NATS routes the event to every instance, kit forwards to all 100 clients per instance. Zero manual fan-out logic. |

## Patterns

### Per-user subscriptions

```go
wsnats.Register(eng, "events.user", svc.NATS,
    func(ctx context.Context, c *fibermap.Context[appCtx]) (wsnats.Bridge, error) {
        return wsnats.Bridge{
            Subscribe: []string{"user." + c.Data.UserID + ".>"},
        }, nil
    })
```

### Read-only ticker feed

```go
wsnats.Register(eng, "ticker.feed", svc.NATS,
    func(ctx context.Context, c *fibermap.Context[appCtx]) (wsnats.Bridge, error) {
        return wsnats.Bridge{
            Subscribe: []string{"market.ticker.*"},
            // Publish: ""  — клиент не отправляет ничего обратно
        }, nil
    })
```

### Echo + custom ACK

```go
wsnats.Register(eng, "chat.room", svc.NATS,
    func(ctx context.Context, c *fibermap.Context[appCtx]) (wsnats.Bridge, error) {
        return wsnats.Bridge{
            Subscribe: []string{"chat.room.42.>"},
            Publish:   "chat.room.42." + c.Data.UserID,
            OnFrame: func(payload []byte) ([]byte, error) {
                // wrap incoming message with sender metadata
                return json.Marshal(struct {
                    From    string          `json:"from"`
                    Payload json.RawMessage `json:"payload"`
                }{c.Data.UserID, payload})
            },
        }, nil
    })
```

### Filtering NATS messages

```go
wsnats.Register(eng, "filtered.feed", svc.NATS,
    func(ctx context.Context, c *fibermap.Context[appCtx]) (wsnats.Bridge, error) {
        return wsnats.Bridge{
            Subscribe: []string{"events.>"},
            OnMessage: func(msg *nats.Msg) ([]byte, error) {
                // drop messages для admin-only event types
                if strings.HasPrefix(msg.Subject, "events.admin.") {
                    if c.Data.Role != "admin" {
                        return nil, nil  // drop silently
                    }
                }
                return msg.Data, nil
            },
        }, nil
    })
```

### QueueGroup для load-balanced fan-in

```go
// Только ОДИН backend instance receives each "worker.job" message;
// the chosen one forwards к its connected WS client. Useful for
// distributing work without duplicate processing.
wsnats.Register(eng, "worker.feed", svc.NATS,
    func(ctx context.Context, c *fibermap.Context[appCtx]) (wsnats.Bridge, error) {
        return wsnats.Bridge{
            Subscribe:  []string{"worker.job"},
            QueueGroup: "ws-workers",
        }, nil
    })
```

## Concurrency safety

- **WS writes serialized** via mutex — multiple NATS subscriptions могут fire concurrently, но `WriteMessage` calls go through a single critical section.
- **Subscriptions unsubscribed on close** — kit defer'ит unsubscribe loop в финальном cleanup'е; нет leak'а subscription'ов даже на panic'ах в handler'ах (websocket.New recovers).
- **Loop ctx cancellation** propagates to NATS message handlers — поздно прибывшая NATS msg на close'нутом WS не will try to write to dead conn.

## См. также

- [`fibermap`](../README.md) — родительский router
- [`fibermap/ws`](../ws/README.md) — direct WS handler (no NATS bridge)
- [`fibermap/sse`](../sse/README.md) — Server-Sent Events (server-to-browser, no client→server)
- [`clients/nats`](../../clients/nats/README.md) — NATS client wrapper
