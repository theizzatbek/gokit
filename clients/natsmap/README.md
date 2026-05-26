# clients/natsmap

Declarative YAML layer over `clients/nats` for NATS / JetStream subscribers and publishers. Subscribers and publishers are described in YAML; Go code registers typed handlers and publishers by name; `Build` returns a goroutine-safe `*Runtime` exposing `Drain()` and `Publish[T](...)`. Symmetric to `clients/apimap` for outbound HTTP.

**Import:** `github.com/theizzatbek/gokit/clients/natsmap`
**Depends on:** `gopkg.in/yaml.v3` + `github.com/theizzatbek/gokit/errs` + `github.com/theizzatbek/gokit/clients/nats`

## Why use it

`clients/nats` gives you typed `Publisher[T]` and `Subscribe[T]` with auto-Ack semantics. It does NOT solve **subject catalog** — every service still hand-writes the same Subscribe call per subject, with the same boilerplate around durable names, MaxInFlight, MaxDeliver, backoff curves, and start-from policies.

`natsmap` is the missing layer: subscribers and publishers live in YAML; the code registers typed handlers and publishes by name. One grep across `*.yaml` answers "what subjects does this service consume? what subjects does it publish?". Symmetric to `clients/apimap` for outbound HTTP — the kit's broader theme of a declarative outer shell wrapping a typed Go core.

## Quickstart

`events.yaml`:

```yaml
subscribers:
  - name: invoice_sender
    subject: orders.created
    durable: invoice-sender
    max_in_flight: 16
    max_deliver: 5
    ack_wait: 30s
publishers:
  - name: orders.created
    subject: orders.created
    headers:
      X-Source: orders-svc
```

`main.go`:

```go
package main

import (
    "context"
    "log/slog"
    "time"

    natsclient "github.com/theizzatbek/gokit/clients/nats"
    "github.com/theizzatbek/gokit/clients/natsmap"
)

type OrderCreated struct {
    ID    string `json:"id"`
    Total int64  `json:"total"`
}

func main() {
    ctx := context.Background()
    logger := slog.Default()

    c, err := natsclient.Connect(ctx, natsclient.Config{
        URL:  "nats://localhost:4222",
        Name: "orders-svc",
    }, natsclient.WithLogger(logger))
    if err != nil {
        panic(err)
    }
    defer c.Close()

    // Idempotent — safe on every startup.
    _ = c.EnsureStream(ctx, natsclient.StreamConfig{
        Name:     "ORDERS",
        Subjects: []string{"orders.>"},
        MaxAge:   7 * 24 * time.Hour,
        Storage:  natsclient.StorageFile,
    })

    eng := natsmap.New()
    if err := eng.LoadFile("events.yaml"); err != nil {
        panic(err)
    }
    natsmap.RegisterHandler[OrderCreated](eng, "invoice_sender",
        func(ctx context.Context, m natsclient.Msg[OrderCreated]) error {
            logger.Info("invoice", "id", m.Data.ID, "total", m.Data.Total)
            return nil // → Ack
        })
    natsmap.RegisterPublisher[OrderCreated](eng, "orders.created")

    rt, err := eng.Build(ctx, c, natsmap.WithLogger(logger))
    if err != nil {
        panic(err)
    }
    defer rt.Drain()

    _ = natsmap.Publish[OrderCreated](ctx, rt, "orders.created",
        OrderCreated{ID: "o1", Total: 4200})
}
```

## YAML schemas

### `subscribers.yaml`

```yaml
subscribers:
  - name: <string>                 # required, unique within engine; pairs with RegisterHandler[T]
    subject: <string>              # required; NATS subject (literal or wildcard)
    durable: <string>              # optional; durable consumer name (survives restart)
    max_in_flight: <int>           # optional; handler concurrency semaphore (>= 0)
    max_deliver: <int>             # optional; total delivery attempts before Term (>= 0)
    ack_wait: <duration>           # optional; redeliver if Ack not seen within this window
    queue_group: <string>          # optional; round-robin across queue members (load balancing)
    backoff:                       # optional; per-redelivery backoff
      type: exponential|fixed      # default "exponential"
      base: <duration>             # required when backoff: is set; must be > 0
      max: <duration>              # optional; defaults to base*32 for exponential, ignored for fixed
    start_from: <policy>           # optional; see "start_from shapes" below
    filter_subject: <string>       # optional; override subject filter on the JetStream consumer
```

### `publishers.yaml`

```yaml
publishers:
  - name: <string>                 # required, unique within engine; pairs with RegisterPublisher[T]
    subject: <string>              # required; NATS subject the publisher targets
    headers:                       # optional; map[string]string applied to every publish
      <Header-Name>: <value>       # expanded to []string{value} when sent
```

### Combined `events.yaml`

Both blocks may live in one file:

```yaml
subscribers:
  - name: invoice_sender
    subject: orders.created
    durable: invoice-sender
publishers:
  - name: orders.created
    subject: orders.created
```

`LoadFile` is additive — calling it multiple times appends entries into one engine. You can keep `subscribers.yaml` and `publishers.yaml` as separate files, or merge them into one `events.yaml`. Both shapes are first-class.

### Env-var substitution

`${VAR_NAME}` anywhere in the YAML is resolved against `os.Getenv` at LoadFile time (regex `[A-Z_][A-Z0-9_]*` — uppercase only). Useful for environment-specific subject prefixes:

```yaml
subscribers:
  - name: invoice_sender
    subject: ${ENV}.orders.created
    durable: invoice-sender-${ENV}
```

| Code | When |
|---|---|
| `natsmap_env_var_unset` | `${FOO}` referenced but `FOO` not in env |
| `natsmap_env_var_malformed` | `${...}` shape doesn't match the regex (e.g. `${lower-case}`) |

### `start_from` shapes

| Value | Meaning |
|---|---|
| `new` (default) | Only deliver messages published after the consumer is created |
| `all` | Replay every message in the stream from the beginning |
| `from_seq:<int>` | Start from the given JetStream sequence number |
| `from_time:<RFC3339>` | Start from the first message at or after the given time (e.g. `from_time:2026-01-15T00:00:00Z`) |

### `backoff` knobs

| Field | Required | Notes |
|---|---|---|
| `type` | yes | `exponential` (default) or `fixed` |
| `base` | yes | Initial delay; for `fixed` it's the only delay |
| `max` | no | Upper cap for `exponential`; defaults to `base * 32`; ignored for `fixed` |

## Public API

```go
type Engine struct{ /* unexported */ }
type Runtime struct{ /* unexported */ }
type Option func(*options)

// Engine lifecycle (build-once)
func New() *Engine
func (e *Engine) LoadFile(path string) error              // additive — call multiple times
func (e *Engine) LoadBytes(b []byte) error                // additive — call multiple times

// Typed registration — panic on duplicate name or post-Build call
func RegisterHandler[T any](e *Engine, name string,
    h func(ctx context.Context, m natsclient.Msg[T]) error)
func RegisterPublisher[T any](e *Engine, name string)

// Build: validates everything, opens subscriptions, returns *Runtime.
// Multiple validation failures are aggregated via errors.Join.
func (e *Engine) Build(ctx context.Context, c *natsclient.Client, opts ...Option) (*Runtime, error)

// Options
func WithLogger(*slog.Logger) Option
func WithMetrics(prometheus.Registerer) Option

// Runtime — goroutine-safe
func Publish[T any](ctx context.Context, r *Runtime, name string, payload T) error
func PublishWithHeaders[T any](ctx context.Context, r *Runtime, name string,
    payload T, headers map[string][]string) error
func (r *Runtime) Drain() error                            // idempotent
func (r *Runtime) SubscriberNames() []string               // sorted
func (r *Runtime) PublisherNames() []string                // sorted
```

`PublishWithHeaders` merges per-call headers over the YAML-declared static headers; per-call entries win on collision.

## Common patterns

### Via `gokit/service`

Set `NATSMAP_SUBSCRIBERS_PATH` / `NATSMAP_PUBLISHERS_PATH` in the environment (one or both is the opt-in trigger). Wire typed registrations with `service.WithNATSMapRegistration`:

```go
svc, err := service.New[ReqCtx, MyClaims](ctx, cfg,
    service.WithNATSMapRegistration(func(e *natsmap.Engine) {
        natsmap.RegisterHandler[OrderCreated](e, "invoice_sender", handleInvoice)
        natsmap.RegisterPublisher[OrderCreated](e, "orders.created")
    }),
)
// svc.NATSMap is the *natsmap.Runtime; svc.Run() drains it on shutdown
// before tearing down the underlying NATS connection.
_ = svc.Run()
```

### Standalone wiring

`Connect → EnsureStream → New → LoadFile → Register* → Build` (see Quickstart above). No service framework required.

### Queue groups for load balancing

```yaml
subscribers:
  - name: invoice_sender
    subject: orders.created
    queue_group: invoice-workers
    durable: invoice-workers
```

Multiple instances of the same service with `queue_group: invoice-workers` share the load: each message is delivered to exactly one queue member, round-robin. Pair `queue_group` with a shared `durable` name to make the consumer survive restarts.

### Mixed YAML files

```go
_ = eng.LoadFile("subscribers.yaml")
_ = eng.LoadFile("publishers.yaml")
// — or —
_ = eng.LoadFile("events.yaml") // combined
```

`LoadFile` accumulates into the same engine. Use whichever layout fits your repo.

### Type mismatch — startup vs runtime

| Failure | When detected | Mechanism |
|---|---|---|
| Duplicate `RegisterHandler[T]` for one name | startup (registration) | panic with `natsmap_duplicate_subscriber` |
| `Register*` after `Build` | startup (registration) | panic with `natsmap_already_built` |
| YAML subscriber with no `RegisterHandler` | startup (Build) | error `natsmap_handler_not_registered` |
| `RegisterHandler` for unknown YAML name | startup (Build) | error `natsmap_handler_unknown` |
| `Publish[WrongType]` at runtime | runtime | error `natsmap_publisher_type_mismatch` |

The intent: every YAML-vs-code mismatch surfaces at Build, before any subscription opens. Wrong-type publishes still surface at the call site so test coverage catches them.

## Error model

All errors are `*errs.Error` with stable `Code`.

### Build-time (collected via `errors.Join`)

| Code | Kind | When |
|---|---|---|
| `natsmap_read_file` | Validation | `LoadFile` cannot read the file |
| `natsmap_parse_yaml` | Validation | YAML decode failure |
| `natsmap_env_var_unset` | Validation | `${VAR}` references unset env var |
| `natsmap_env_var_malformed` | Validation | `${...}` doesn't match `[A-Z_][A-Z0-9_]*` |
| `natsmap_no_entries` | Validation | YAML parsed but has no subscribers and no publishers |
| `natsmap_missing_name` | Validation | subscriber/publisher entry without `name` |
| `natsmap_missing_subject` | Validation | subscriber/publisher entry without `subject` |
| `natsmap_duplicate_subscriber` | Validation | two subscribers share `name` |
| `natsmap_duplicate_publisher` | Validation | two publishers share `name` |
| `natsmap_invalid_max_in_flight` | Validation | `max_in_flight < 0` |
| `natsmap_invalid_max_deliver` | Validation | `max_deliver < 0` |
| `natsmap_invalid_ack_wait` | Validation | `ack_wait < 0` |
| `natsmap_invalid_backoff` | Validation | `backoff.type` unknown, `base <= 0`, or `max < base` |
| `natsmap_invalid_start_from` | Validation | `start_from` outside `new|all|from_seq:<int>|from_time:<RFC3339>` |
| `natsmap_handler_not_registered` | Validation | YAML subscriber has no matching `RegisterHandler[T]` |
| `natsmap_handler_unknown` | Validation | `RegisterHandler` for name not in YAML |
| `natsmap_publisher_not_registered` | Validation | YAML publisher has no matching `RegisterPublisher[T]` |
| `natsmap_publisher_unknown` | Validation | `RegisterPublisher` for name not in YAML |
| `natsmap_subscribe_failed` | Unavailable | underlying `natsclient.SubscribeRaw` failed |
| `natsmap_already_built` | Validation | `Build` called twice, or `Register*` after `Build` |

### Runtime (from `Publish` / `PublishWithHeaders`)

| Code | Kind | When |
|---|---|---|
| `natsmap_unknown_publisher` | NotFound | `name` not in YAML / not registered |
| `natsmap_publisher_type_mismatch` | Validation | `Publish[T]` `T` differs from the registered type |
| `natsmap_publish_failed` | Unavailable | underlying `natsclient` publish returned an error |

## Observability

### `WithLogger`

`WithLogger(*slog.Logger)` sets the logger natsmap uses for natsmap-level events (currently registration warnings; future hot-reload). Per-subscription handler logs — decode failures (→ Term), handler errors (→ Nak with backoff), max-deliver exceeded — are owned by `clients/nats`. Configure that one too by passing the same logger to `natsclient.Connect(..., natsclient.WithLogger(logger))`.

### `WithMetrics`

`WithMetrics(prometheus.Registerer)` is accepted for symmetry with `apimap`. natsmap itself currently exposes no collectors; subscription/publish metrics (in-flight gauge, handler success/error counter, decode-error counter, publish-duration histogram) come from `clients/nats.WithMetrics`.

```go
c, _ := natsclient.Connect(ctx, cfg,
    natsclient.WithLogger(logger),
    natsclient.WithMetrics(promReg),
)
rt, _ := eng.Build(ctx, c,
    natsmap.WithLogger(logger),   // for future natsmap-level events
    natsmap.WithMetrics(promReg), // reserved
)
```

## Testing

Unit tests run without Docker:

```bash
go test -short ./clients/natsmap/
```

Integration smoke (`TestRuntime_PublishAndReceive`, `TestRuntime_BuildAggregatesValidationErrors`, `TestRuntime_BuildTwiceFails`) spins up `nats:2-alpine` with `-js` via `testcontainers-go/modules/nats` — Docker required:

```bash
go test ./clients/natsmap/
```

For your own tests, follow the same pattern: spin up the testcontainer, `EnsureStream`, `LoadBytes` an inline YAML, `Register*`, `Build`, `defer rt.Drain()`.

## Limitations

- **No hot-reload of YAML.** Loaded once at startup. A future `WithHotReload()` is planned.
- **`Msg[T].Raw()` returns nil for natsmap-routed messages.** The reflection bridge decodes payloads into freshly-allocated `*T` and never retains the underlying `*nats.Msg`. If you need raw access (headers manipulation, manual Ack timing, JetStream metadata beyond what `Msg[T]` exposes), use `natsclient.Subscribe[T]` directly.
- **One codec per `*natsclient.Client`.** Inherited from `clients/nats`. Heterogeneous wire formats across topics require multiple clients.
- **No "web publisher" yet.** A future package will bridge a `fibermap` route to a NATS publish in YAML; out of scope here.
- **`Build` opens every subscription synchronously.** A long subscriber list with slow JetStream creates a slow startup; failures aggregate via `errors.Join`.
- **No subject-name validation against the stream.** If `subject:` doesn't match a stream configured on the server, the underlying `Subscribe` fails at Build with `natsmap_subscribe_failed`.

## See also

- [`clients/nats`](../nats/README.md) — typed JetStream wrapper underlying natsmap (Publisher[T], Subscribe[T], EnsureStream)
- [`clients/apimap`](../apimap/README.md) — symmetric declarative HTTP layer (the inbound/outbound analogue)
- [`service`](../../service/README.md) — auto-wires natsmap when `NATSMAP_SUBSCRIBERS_PATH` / `NATSMAP_PUBLISHERS_PATH` are set
- [`errs`](../../errs/README.md) — error contract
- [`examples/urlshort`](../../examples/urlshort/README.md) — uses natsmap for `urlshort.link.{created,visited}` publishers + subscribers
