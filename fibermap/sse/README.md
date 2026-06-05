# fibermap/sse

Server-Sent Events handlers для `fibermap`. Subпакет живёт за пределами core fibermap, чтобы основной router не таскал streaming-specific code paths (хотя SSE dep-free — только fiber + stdlib).

**Импорт:** `github.com/theizzatbek/gokit/fibermap/sse`
**Зависит от:** `fibermap`, `valyala/fasthttp` (transitively через fiber)

## Quickstart

```go
import (
    "context"
    "github.com/theizzatbek/gokit/fibermap"
    "github.com/theizzatbek/gokit/fibermap/sse"
)

type appCtx struct{ Subject string }

eng := fibermap.New[appCtx]()
eng.SetContextBuilder(buildAppCtx)

sse.Register(eng, "events.stream",
    func(ctx context.Context, c *fibermap.Context[appCtx], s *sse.Stream) error {
        for {
            select {
            case <-ctx.Done():
                return nil
            case msg := <-events:
                if err := s.SendJSON("update", msg); err != nil {
                    return err // client disconnected
                }
            }
        }
    })
```

И в `routes.yaml`:

```yaml
- method: GET
  path:   /events
  handler: events.stream
  middleware:
    - auth: required   # runs ПЕРЕД SSE wrap'ом
```

## Что делает Register

1. Регистрирует под `name` обычный fibermap handler.
2. Wrapped handler:
   - Выставляет SSE response-headers (`Content-Type: text/event-stream`, `Cache-Control: no-cache, no-store`, `Connection: keep-alive`, `X-Accel-Buffering: no`);
   - Wires fasthttp's `SetBodyStreamWriter`;
   - Вызывает user's `HandlerFunc[T]` с request's UserContext и kit-managed `*Stream`.

Middleware chain маршрута runs BEFORE SSE wrap — auth / rate-limit / etc. могут отвергнуть запрос с обычным HTTP error response.

## Stream API

```go
type Stream struct { ... }

// Один SSE-event frame:
//   event: <event>
//   data: <data>
// Auto-flush'ит после каждого Send.
func (s *Stream) Send(event, data string) error

// JSON-encoded variant; Marshal'ит payload then forwards в Send.
func (s *Stream) SendJSON(event string, payload any) error

// Comment-frame (`: <text>`); используется для keep-alive ping'ов
// на idle streams — большинство browsers/intermediaries закрывают
// HTTP connection после ~60s no-traffic.
func (s *Stream) Comment(text string) error

// Первая ошибка из Send/SendJSON/Comment. Useful для handler'ов,
// которые want detect disconnect без checking каждого Send return.
func (s *Stream) Err() error
```

Stream **не safe для concurrent use** — pin одну goroutine per Stream. Если нужно multi-publisher SSE, используйте channel + single fan-in goroutine.

## Multiline data

Per SSE spec, data с CR/LF разбивается kit'ом на отдельные `data:` lines:

```go
s.Send("", "line1\nline2\nline3")
```

→ wire format:

```
data: line1
data: line2
data: line3

```

Empty event-name (`""`) — kit suppresses `event:` line (client получает default `message` event).

## Disconnect detection

Когда client закрывает connection mid-stream:
- Underlying `bufio.Writer.Flush()` returns error;
- Stream stores ошибку internally + subsequent Send/SendJSON/Comment return that error без attempted write;
- Handler should bail out of loop on first non-nil return.

Kit НЕ cancels ctx когда client disconnects — disconnect surfaces only via Send return value. Pair `Send` errors with `ctx.Done()` selection для clean shutdown.

## Keep-alive

Browsers / proxies often kill idle HTTP-connections после ~60s. Send `Comment("ping")` периодически:

```go
go func() {
    tick := time.NewTicker(30 * time.Second)
    defer tick.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-tick.C:
            _ = stream.Comment("keepalive")
        }
    }
}()
```

(Caveat: Stream not concurrent-safe — funnel writes through one goroutine, see above.)

## См. также

- [`fibermap`](../README.md) — родительский router
- [`fibermap/ws`](../ws/README.md) — WebSocket equivalent (нужен `gofiber/websocket/v2`)
