# clients/nats

Typed NATS / JetStream client wrapper (package `natsclient`). `Connect(ctx, cfg, opts...) (*Client, error)` opens a connection + JetStream context. Generic `Publisher[T]` / `Subscribe[T]` over an opt-in `Codec` (JSON default). Auto-ack handler model: handler returns nil → Ack, err → Nak with exponential backoff, decode-fail → Term (poison pill). `MaxInFlight` semaphore caps handler concurrency. Idempotent `EnsureStream` for app-managed stream lifecycle.

**Import:** `github.com/theizzatbek/gokit/clients/nats` (package `natsclient` — naming avoids collision with `nats-io/nats.go`)
**Depends on:** `nats-io/nats.go` + `prometheus/client_golang` + `github.com/theizzatbek/gokit/errs`

## Why use it

The raw `nats.go` API is `[]byte`-based and untyped. Every service ends up writing the same publish-encode-with-codec and subscribe-decode-Ack-Nak boilerplate, with subtle bugs around "what if decode fails forever — do we infinitely redeliver?". `natsclient` is that bundle: typed `Publisher[T]` + `Subscribe[T]` with auto-Ack, opinionated handler model (decode fail → Term, not redeliver), MaxInFlight backpressure, and stable error mapping into `*errs.Error`.

## Quickstart

```go
import (
    "context"
    "time"
    natsclient "github.com/theizzatbek/gokit/clients/nats"
)

type OrderCreated struct {
    ID     string `json:"id"`
    Amount int64  `json:"amount"`
}

func main() {
    c, err := natsclient.Connect(ctx, natsclient.Config{
        URL:  "nats://localhost:4222",
        Name: "orders-svc",
    }, natsclient.WithLogger(logger), natsclient.WithMetrics(promReg))
    if err != nil { return err }
    defer c.Close()

    // Idempotent — safe on every startup.
    err = c.EnsureStream(ctx, natsclient.StreamConfig{
        Name:     "ORDERS",
        Subjects: []string{"orders.>"},
        MaxAge:   7 * 24 * time.Hour,
        Storage:  natsclient.StorageFile,
    })

    // Producer
    pub := natsclient.NewPublisher[OrderCreated](c)
    pub.Publish(ctx, "orders.created", OrderCreated{ID: "abc", Amount: 100})

    // Consumer
    sub, err := natsclient.Subscribe[OrderCreated](ctx, c, "orders.created",
        func(ctx context.Context, m natsclient.Msg[OrderCreated]) error {
            if err := sendInvoice(ctx, m.Data); err != nil {
                return err  // → Nak with exponential backoff
            }
            return nil      // → Ack
        },
        natsclient.WithDurable("invoice-sender"),
        natsclient.WithMaxInFlight(16),
        natsclient.WithMaxDeliver(5),
    )
    defer sub.Drain()
}
```

## Configuration

### `natsclient.Config`

| Field | Default | Notes |
|---|---|---|
| `URL` | — (required) | `nats://host:port`, comma-separated for cluster |
| `Name` | filepath.Base(os.Args[0]) | Client name visible in NATS monitoring |
| `Timeout` | 5s | Connect timeout |
| `Token` | "" | Token auth (pick at most ONE auth method) |
| `User`, `Password` | "" | Basic auth (both required together) |
| `CredsFile` | "" | NATS 2.0+ JWT creds file path |
| `NKeySeed` | "" | Raw NKey seed |
| `MaxReconnects` | -1 (infinite) | Set positive to give up |
| `ReconnectWait` | 2s | Delay between reconnect attempts |

### Options

| Option | Default | Notes |
|---|---|---|
| `WithCodec(Codec)` | `JSONCodec` | Wire format for ALL publishers and subscribers |
| `WithLogger(*slog.Logger)` | silent | Reconnect/disconnect events, handler errors, decode failures |
| `WithMetrics(prometheus.Registerer)` | no collectors | publish/decode/handler counters + histograms |
| `WithReconnectHandler(fn)` | none | Fires after each successful reconnect |
| `WithDisconnectErrHandler(fn)` | none | Fires on each disconnect |
| `WithClosedHandler(fn)` | none | Fires when connection is permanently closed |

## Common patterns

### Stream lifecycle — `EnsureStream`

`EnsureStream` is idempotent: creates the stream if absent, validates config matches if present, returns the existing stream otherwise. Safe to call on every startup.

```go
err := c.EnsureStream(ctx, natsclient.StreamConfig{
    Name:      "ORDERS",
    Subjects:  []string{"orders.>"},
    Retention: natsclient.RetentionLimits,  // Limits | Interest | WorkQueue
    Storage:   natsclient.StorageFile,      // File | Memory
    MaxAge:    7 * 24 * time.Hour,
    MaxBytes:  10 * 1024 * 1024 * 1024,    // 10 GiB
    MaxMsgs:   1_000_000,
    Replicas:  3,
    Dedup:     2 * time.Minute,             // server-side Nats-Msg-Id dedup window
})
```

If a stream with the same name exists with a different config, `EnsureStream` returns `*errs.Error{Code: "stream_config_invalid"}` — explicit failure so you don't silently run on the wrong config.

### Publishing

```go
pub := natsclient.NewPublisher[OrderCreated](c)
if err := pub.Publish(ctx, "orders.created", evt); err != nil {
    // *errs.Error{Code: "publish_failed"} on JetStream rejection,
    // *errs.Error{Code: "encode_failed"} on codec error
}

// With Nats-Msg-Id for dedup
err := pub.PublishWithHeaders(ctx, "orders.created", evt, map[string][]string{
    "Nats-Msg-Id": {evt.ID},
})
```

Publishes go through JetStream (subjects matching a stream) or core NATS (others) automatically — `Publisher` introspects the connected stream config.

### Subscribing — auto-ack handler model

```go
sub, err := natsclient.Subscribe[OrderCreated](ctx, c, "orders.created",
    func(ctx context.Context, m natsclient.Msg[OrderCreated]) error {
        return processOrder(ctx, m.Data)  // nil → Ack, err → Nak
    },
    natsclient.WithDurable("invoice-sender"),
    natsclient.WithMaxInFlight(16),
    natsclient.WithMaxDeliver(5),
    natsclient.WithAckWait(30*time.Second),
    natsclient.WithBackoff(func(redeliveries int) time.Duration {
        // exponential 1s, 5s, 25s, …
        d := time.Duration(1<<redeliveries) * time.Second
        if d > time.Minute { return time.Minute }
        return d
    }),
)
defer sub.Drain()  // graceful: stop pulling, finish in-flight, ack remaining
```

| Handler returns | Action |
|---|---|
| `nil` | Ack |
| non-nil `error` | Nak (with backoff if configured) |
| decode failure (before handler runs) | Term — poison pill, removed permanently from stream |
| panic | Recovered, treated as error → Nak |

After `WithMaxDeliver` is exceeded, the message is `Term`'d and logged at ERROR.

### Subscribe options

| Option | Default | Notes |
|---|---|---|
| `WithDurable(name)` | empty (ephemeral) | JetStream durable consumer name — survives subscriber restart |
| `WithMaxInFlight(n)` | 1 | Concurrent handlers semaphore (backpressure) |
| `WithAckWait(d)` | 30s | NATS redelivers if Ack not seen within `d` |
| `WithMaxDeliver(n)` | 5 | Total delivery attempts before Term |
| `WithBackoff(fn)` | exponential | `fn(redeliveries) time.Duration` |
| `WithStartFrom(StartPolicy)` | StartNew | Where the consumer starts: `StartNew` / `StartAll` / `StartFromTime(t)` / `StartFromSequence(seq)` |
| `WithFilterSubject(s)` | subject from call | Override subject filter for the consumer |
| `WithQueueGroup(g)` | none | Distributed work-queue semantics (round-robin across queue members) |

### Custom codec (e.g. protobuf)

```go
type ProtoCodec struct{}
func (ProtoCodec) Encode(v any) ([]byte, error) { return proto.Marshal(v.(proto.Message)) }
func (ProtoCodec) Decode(data []byte, v any) error { return proto.Unmarshal(data, v.(proto.Message)) }

c, _ := natsclient.Connect(ctx, cfg, natsclient.WithCodec(ProtoCodec{}))
```

One codec per Client — keeps the wire format consistent service-wide.

## Error model

All errors are `*errs.Error` with stable `Code`:

| Code | Kind | When |
|---|---|---|
| `connect_failed` | Unavailable | Initial connect (DNS, refused, auth fail) |
| `jetstream_unavailable` | Unavailable | JetStream context unreachable |
| `missing_url` / `auth_ambiguous` | Validation | Config errors at Connect |
| `invalid_nkey` | Validation | NKeySeed unparseable |
| `stream_not_found` / `stream_op_failed` / `stream_config_invalid` | Various | EnsureStream + stream ops |
| `consumer_op_failed` | Various | Subscribe / consumer ops |
| `publish_failed` | Unavailable | JetStream / NATS publish failure |
| `encode_failed` / `decode_failed` | Internal | Codec failures |

## Observability

### slog

- `Info "natsclient connect"` on first connect
- `Warn "natsclient disconnected"` on each disconnect
- `Info "natsclient reconnected"` on each successful reconnect
- `Warn "natsclient handler error"` (on Nak)
- `Error "natsclient decode failed"` (poison pill Term)
- `Warn "natsclient max deliver exceeded"` (Term after retries)

### Prometheus (opt-in)

| Metric | Type | Labels |
|---|---|---|
| `natsclient_published_total` | counter | `subject`, `status` |
| `natsclient_publish_duration_seconds` | histogram | `subject` |
| `natsclient_handled_total` | counter | `subject`, `result` (`ack`/`nak`/`term`) |
| `natsclient_handler_duration_seconds` | histogram | `subject` |
| `natsclient_in_flight` | gauge | `subject` |

## Testing

Use [testcontainers-go/modules/nats](https://golang.testcontainers.org/modules/nats/):

```go
c, _ := tcnats.Run(ctx, "nats:2-alpine", testcontainers.WithCmd("-js"))
t.Cleanup(func() { _ = c.Terminate(ctx) })
url, _ := c.ConnectionString(ctx)

client, _ := natsclient.Connect(ctx, natsclient.Config{URL: url, Name: "test"})
defer client.Close()
client.EnsureStream(ctx, natsclient.StreamConfig{
    Name: "TEST", Subjects: []string{"test.>"},
})

// subscribe + publish + assert
```

For per-test isolation, use unique stream + subject names per test.

## Limitations

- **JetStream-first design.** Subjects covered by no stream auto-use core NATS (best-effort). If you need exclusively core NATS, use raw `nats.go` and skip this wrapper.
- **One codec per Client.** Heterogeneous wire formats across topics require multiple `*Client` instances or a custom codec dispatching internally.
- **Auto-ack model is opinionated.** Handler returns nil → Ack. No "explicit Ack later from a goroutine" — by design (avoids leaked Ack budget).
- **`WithMaxInFlight` is local, not stream-wide.** For stream-wide backpressure use JetStream's own MaxAckPending on the consumer.
- **No multi-stream subjects via one Subscribe.** One subscription = one subject (or NATS wildcard) on one stream.
- **`Drain` blocks** until in-flight handlers finish. For force-shutdown use `Close` (loses in-flight).

## See also

- [`errs`](../../errs/README.md) — error contract
- [`examples/nats`](../../examples/nats/) — minimal publish + subscribe example
- [`examples/urlshort`](../../examples/urlshort/README.md) — uses natsclient for `urlshort.link.{created,visited}` publish
