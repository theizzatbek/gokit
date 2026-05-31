# redisclient

Thin Redis bootstrap for kit-based services. One call opens a
`*redis.Client`, retries the initial PING with exponential backoff,
and (when wired) routes every command through a Prometheus +
`*slog.Logger` hook.

**Import:** `github.com/theizzatbek/gokit/clients/redis`
**Depends on:** `github.com/redis/go-redis/v9`

Package name is `redisclient` to avoid colliding with go-redis's
own `redis` package — same convention as `clients/nats` →
`natsclient`.

## Quickstart

```go
cli, err := redisclient.Connect(ctx, redisclient.Config{
    URL:               "redis://localhost:6379",
    ConnectMaxRetries: 5,
},
    redisclient.WithLogger(logger),
    redisclient.WithMetrics(reg),
)
if err != nil { return err }
defer cli.Close()

rdb := cli.Redis()  // *redis.Client — full go-redis surface
```

`service.New` auto-wires this when `service.Config.Redis.URL` is
set — exposes the result as `svc.Redis`.

## Config

| Field | Env (with `REDIS_` prefix in service) | Notes |
|---|---|---|
| `URL` | `REDIS_URL` | Required. `redis://[user:pass@]host:port[/db]`, `rediss://…` for TLS. |
| `ConnectMaxRetries` | `REDIS_CONNECT_MAX_RETRIES` | 0 = no retry (one attempt). service auto-defaults to 5 when zero; pass `-1` to disable. |
| `ConnectBackoffBase` | `REDIS_CONNECT_BACKOFF_BASE` | Doubles each attempt. service injects 1s when zero. |
| `ConnectBackoffMax` | `REDIS_CONNECT_BACKOFF_MAX` | Caps the per-attempt wait. service injects 16s when zero. |

## Options

| Option | Notes |
|---|---|
| `WithLogger(*slog.Logger)` | Wires the connect-retry Warn + per-command observability via the go-redis Hook. |
| `WithMetrics(prometheus.Registerer)` | Registers `redis_commands_total{cmd,outcome}`, `redis_command_duration_seconds{cmd}`, and `redis_pool_size_total{state}`. |
| `WithRedisOptions(fn)` | Mutator for the parsed `*redis.Options` (PoolSize, MinIdleConns, ReadTimeout, custom TLSConfig — anything not expressible in the URL). |

## Observability

`WithMetrics` installs a go-redis Hook that records every command
issued through `Client.Redis()` — including commands by user code,
not just kit-issued ones.

- `redis_commands_total{cmd, outcome}` — counter. `outcome="error"`
  excludes `redis.Nil` (the "key not found" sentinel is operational
  success).
- `redis_command_duration_seconds{cmd}` — histogram, default
  buckets.
- `redis_pool_size_total{state="hits|misses|idle|stale|total"}` —
  gauge, refreshed from `PoolStats()` on every scrape.

## Errors

| Code | When |
|---|---|
| `redis_missing_url` | `Config.URL` empty at `Connect`. |
| `redis_invalid_url` | `redis.ParseURL` rejected the URL. |
| `redis_connect_failed` | PING failed after exhausting `ConnectMaxRetries`. Wraps the last underlying error. |

All three propagate as `*errs.Error` with `Kind` set:
`KindValidation` for the first two, `KindUnavailable` for the
third. Service maps the connect failure to
`service_redis_connect_failed` via `Wrap`.

## See also

- [`clients/cache`](../cache/README.md) — typed Get/Set/SetNotFound/
  Invalidate cache layered on top of any `*redis.Client`.
- [`service`](../../service/README.md) — `service.Config.Redis` +
  `WithRedisOptions(...)` for the bundled wiring.
