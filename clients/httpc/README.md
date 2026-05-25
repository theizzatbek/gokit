# clients/httpc

Outbound HTTP client builder. `httpc.New(cfg, opts...)` returns a stdlib `*http.Client` whose transport chain wraps the user-supplied or `http.DefaultTransport` with per-attempt timeout, full-jitter exponential retry on transient failures, and opt-in slog/Prometheus observability. Returns a stdlib `*http.Client` so it composes with any library that wants that type (AWS SDK, Stripe, OAuth libs, …).

**Import:** `github.com/theizzatbek/gokit/clients/httpc`
**Depends on:** stdlib + `prometheus/client_golang` + `github.com/theizzatbek/gokit/errs`

## Why use it

Outbound HTTP boilerplate is the same in every service: a `Timeout`-bounded `*http.Client`, retry with backoff on transient failures, honour `Retry-After`, log at the right level, expose metrics. `httpc` is that bundle, exposed via `Config` + functional options. Returns the standard `*http.Client` so any SDK that accepts one works unchanged.

## Quickstart

```go
import (
    "time"
    "github.com/theizzatbek/gokit/clients/httpc"
)

c, err := httpc.New(httpc.Config{
    Timeout:     10 * time.Second,
    MaxRetries:  3,
    BackoffBase: 100 * time.Millisecond,
    BackoffMax:  5 * time.Second,
}, httpc.WithLogger(logger), httpc.WithMetrics(promReg))
if err != nil { return err }

resp, err := c.Get("https://api.example.com/users/42")
```

Everything works as if it were `http.DefaultClient` — retries happen transparently for idempotent methods on transient failures; the response is plain `*http.Response`.

## Configuration

### `httpc.Config`

| Field | Default | Notes |
|---|---|---|
| `Timeout` | 10s | Per-attempt deadline (via `context.WithTimeout`). Wall-clock budget across retries is the caller's `req.Context()` deadline. |
| `MaxRetries` | 3 (when omitted) | Number of *additional* attempts after the first. Pass `-1` to disable retries entirely. |
| `BackoffBase` | 100ms | Initial exponential delay. Jitter is `rand.Float64() * min(base * 2^attempt, max)` |
| `BackoffMax` | 5s | Cap on the exponential growth |

### Options

| Option | Default | Notes |
|---|---|---|
| `WithLogger(*slog.Logger)` | silent | Debug per retry decision, Warn on retry exhaustion |
| `WithMetrics(prometheus.Registerer)` | no collectors | Registers requests_total / request_duration_seconds / retries_total / retries_exhausted_total |
| `WithBaseTransport(http.RoundTripper)` | `http.DefaultTransport` | Override the bottom of the chain — layer otel-instrumented or auth-injecting RoundTrippers underneath the retry logic |

## Retry semantics (hard-coded — no overrides)

- **Idempotent methods only:** GET, HEAD, PUT, DELETE, OPTIONS retry. POST and PATCH return after attempt 0 — never silently double-write.
- **Retryable statuses:** 408, 429, 500, 502, 503, 504. Anything else (incl. 4xx) returns immediately.
- **Network errors:** any error from the inner `RoundTrip` (DNS failure, connect refused, EOF mid-stream) retries.
- **Backoff:** `delay = rand.Float64() * min(BackoffBase * 2^attempt, BackoffMax)`. Full jitter — minimises thundering herd.
- **`Retry-After`:** parsed (integer seconds or HTTP-date). If present, used instead of jittered backoff, capped at `4 * BackoffMax`.
- **Body replay:** only when `req.GetBody != nil`. `http.NewRequest` with `bytes.Reader`/`bytes.Buffer`/`strings.Reader` sets it automatically. Streaming bodies (manually constructed `Request{Body: …}`) skip retry after attempt 0.
- **Context cancellation:** preempts both attempts AND backoff sleeps.
- **Exhausted retries:** return the last `(resp, err)` as-is. Caller sees standard `*http.Response` (or stdlib `net.Error`), not `*errs.Error`. Metric `httpc_retries_exhausted_total` increments.

## Common patterns

### Cancellable per-call timeout

`Config.Timeout` is per-attempt. For a total budget across retries, use `context.WithTimeout` at the call site:

```go
ctx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
defer cancel()
req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
resp, err := c.Do(req)
```

### Custom base transport (otel, auth)

```go
// Outermost: otel instrumentation
// Middle:    httpc retry/timeout
// Innermost: auth header injection
auth := authRoundTripper{token: token}
base := otelhttp.NewTransport(auth, otelhttp.WithSpanNameFormatter(...))

c, _ := httpc.New(httpc.Config{Timeout: 5*time.Second, MaxRetries: 2},
    httpc.WithBaseTransport(base),
)
// Or get just the transport for embedding into your own *http.Client:
rt, _ := httpc.NewTransport(cfg, httpc.WithBaseTransport(base))
myClient := &http.Client{Transport: rt}
```

### Disabling retries

```go
httpc.New(httpc.Config{Timeout: 5*time.Second, MaxRetries: -1})
```

`-1` is the sentinel for "no retries — single attempt only". The zero value (`0`) defaults to 3 because the most common mistake is forgetting to set it; opt out explicitly with `-1`.

### Drop-in for SDKs

Anything that takes a `*http.Client` works:

```go
c, _ := httpc.New(httpc.Config{...})
s3 := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
    o.HTTPClient = c
})
```

## Error model

`*errs.Error` only on config validation from `New`/`NewTransport`:

| Code | When |
|---|---|
| `httpc_invalid_timeout` | `Timeout < 0` |
| `httpc_invalid_max_retries` | `MaxRetries < -1` |
| `httpc_invalid_backoff` | `BackoffBase` / `BackoffMax` invalid or `BackoffMax < BackoffBase` |

Runtime errors are stdlib (`*url.Error`, `net.Error`, etc.) — that's the *point* of returning `*http.Client`. If your handler wants to convert "retry exhausted on 503" into a domain error, wrap manually:

```go
resp, err := c.Get(url)
if err != nil { return errs.Wrap(err, errs.KindUnavailable, "upstream_down", "upstream HTTP call failed") }
if resp.StatusCode >= 500 {
    return errs.Internalf("upstream_5xx", "upstream returned %d", resp.StatusCode)
}
```

## Observability

### slog

- `Debug "httpc retry"` — per retry decision: `method`, `url`, `attempt`, `delay_ms`, `status`/`err`/`reason="retry_after"`
- `Warn "httpc retries exhausted"` — at end of exhausted attempts

Successful responses are NOT logged (otel's job).

### Prometheus (opt-in via `WithMetrics`)

| Metric | Type | Labels |
|---|---|---|
| `httpc_requests_total` | counter | `method`, `status` (status="error" for network failures) |
| `httpc_request_duration_seconds` | histogram (DefBuckets) | `method`, `status` |
| `httpc_retries_total` | counter | `method`, `classification` (`5xx`/`429`/`408`/`network`/`retry_after`) |
| `httpc_retries_exhausted_total` | counter | `method` |

`path` is deliberately omitted — high-cardinality. Wrap your own per-endpoint middleware if needed.

## Why net/http and not fasthttp?

Even though fiber (and thus fibermap) is built on fasthttp, outbound stays on `net/http`:

1. **Interop:** AWS SDK, Stripe, every OAuth/JWKS library accepts `*http.Client`. Returning `*fasthttp.Client` would force you to choose between our retry layer and every SDK.
2. **RoundTripper ecosystem:** otel HTTP instrumentation, Prometheus middleware, auth round-trippers — all `http.RoundTripper`. Fasthttp has no equivalent.
3. **Use case:** fasthttp optimises for high-throughput inbound. Client-side throughput for typical microservice outbound is rarely the bottleneck.

Server-side fasthttp (fiber) is an asymmetry that's fine — different problems.

## Testing

Test against `httptest.NewServer`:

```go
func TestRetryOn503(t *testing.T) {
    var n atomic.Int32
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if n.Add(1) < 3 {
            w.WriteHeader(503)
            return
        }
        w.WriteHeader(200)
    }))
    t.Cleanup(srv.Close)

    c, _ := httpc.New(httpc.Config{
        Timeout: time.Second, MaxRetries: 3,
        BackoffBase: time.Millisecond, BackoffMax: 10 * time.Millisecond,
    })
    resp, err := c.Get(srv.URL)
    if err != nil || resp.StatusCode != 200 { t.Fatal(err, resp.StatusCode) }
    if n.Load() != 3 { t.Errorf("got %d, want 3", n.Load()) }
}
```

Use small `BackoffBase`/`BackoffMax` in tests to keep them fast.

## Limitations

- **Retry policy is hard-coded.** Idempotent-only, fixed status set. No `WithRetryClassifier` yet — would land as additive feature if needed.
- **No JSON helpers.** Decode in your handler (`json.NewDecoder(resp.Body).Decode(&out)`). The package stays a transport.
- **No circuit breaker.** Use a separate library or wrap your own RoundTripper.
- **Per-host concurrency caps live on `http.Transport`.** Configure via `WithBaseTransport(custom)` if you need them.
- **Body buffering for streaming bodies without `GetBody` is the caller's job.** httpc won't silently consume + buffer arbitrary upload streams.

## See also

- [`clients/apimap`](../apimap/README.md) — declarative outbound layer built on top of httpc
- [`errs`](../../errs/README.md) — error contract for validation failures
- [`examples/urlshort`](../../examples/urlshort/README.md) — uses httpc for arbitrary URL fetching in the enrich package
