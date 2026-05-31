# cache

Typed Redis-backed read-through cache. Generic over the value type
T; supplies positive/negative caching with TTL knobs, prefix
namespacing, JSON encoding, and best-effort error handling so
callers never have to defend against transient Redis failures.

**Import:** `github.com/theizzatbek/gokit/clients/cache`
**Depends on:** `github.com/redis/go-redis/v9` (raw client supplied
by the caller — typically from `redisclient.Client.Redis()`)

## Quickstart

```go
type User struct {
    ID   string `json:"id"`
    Name string `json:"name"`
}

// One-liner via cache.For — auto-wires the logger from the
// kit's *redisclient.Client, returns nil when svc.Redis is nil
// (cache methods are nil-receiver-safe), panics with *errs.Error
// on an empty KeyPrefix (programmer error, fail-fast at startup).
c := cache.For[User](svc.Redis, "session:user:")
```

Or with full control over TTLs / custom logger / a raw
`*redis.Client`:

```go
c, err := cache.New[User](svc.Redis.Redis(), cache.Config{
    KeyPrefix:   "session:user:",
    PositiveTTL: time.Hour,
    NegativeTTL: time.Minute,
    Logger:      svc.Logger(),
})
if err != nil { return err }

hit := c.Get(ctx, "u-42")
switch {
case hit.Value != nil:      // positive hit
case hit.NotFound:           // negative hit ("known bad")
default:                     // miss → fall through to DB
    u, err := db.LoadUser(ctx, "u-42")
    if errors.Is(err, sql.ErrNoRows) {
        c.SetNotFound(ctx, "u-42")
        return nil, NotFound
    }
    c.Set(ctx, "u-42", u)
}
```

## API

```go
func New[T any](rdb *redis.Client, cfg Config) (*Redis[T], error)

type Lookup[T any] struct {
    Value    *T
    NotFound bool
}

func (c *Redis[T]) Get(ctx, key)        Lookup[T]
func (c *Redis[T]) Set(ctx, key, T)
func (c *Redis[T]) SetNotFound(ctx, key)
func (c *Redis[T]) Invalidate(ctx, key)
```

`Lookup` is tri-state:

| `Value` | `NotFound` | Meaning |
|---|---|---|
| non-nil | false | positive hit; use Value |
| nil | true | negative hit; treat as not-found without touching the source |
| nil | false | miss; query the source |

## Config

| Field | Default | Notes |
|---|---|---|
| `KeyPrefix` | — | Required. Stored keys are `KeyPrefix + key`. Namespace per value type AND per service when sharing a Redis instance. |
| `PositiveTTL` | 1h | Positive entries expire after this. |
| `NegativeTTL` | 60s | Negative-cache sentinel TTL. 0 → default 60s; set explicitly to a very small value to effectively disable. |
| `Logger` | nil (silent) | Receives Warn entries on Redis transport or encode/decode failures. |

## Best-effort error policy

Every Redis-side error is **logged + swallowed**:

- `Get` on transport error → miss. Caller falls through to the source.
- `Set` / `SetNotFound` / `Invalidate` on transport error → log + return. Source of truth is unchanged.
- JSON encode/decode failures → log + miss (Get) or noop (Set).

This is intentional. A cache that propagates errors forces every
call site into a defensive double-path; treating a transient Redis
hiccup as a miss keeps callers' code linear.

## Negative caching

`SetNotFound(ctx, key)` stores a tiny sentinel under the key so the
next `Get` returns `Lookup{NotFound: true}` without checking the
source of truth. Killer feature for 404-absorbing scanner traffic
on public endpoints — pair with route-level rate limiting and a
short `NegativeTTL` (60s default) so a later `Create` takes effect
within the window.

## nil-receiver safety

A `(*Redis[T])(nil)` is safe on every method:

- `Get` returns a zero `Lookup{}` (miss).
- `Set` / `SetNotFound` / `Invalidate` no-op.

Lets you thread a cache reference through services unconditionally;
the "cache off" path is "don't construct one and pass nil".

## See also

- [`clients/redis`](../redis/README.md) — kit-thin Redis client
  wrapper that produces the `*redis.Client` this package consumes.
- [`service`](../../service/README.md) — `service.Config.Redis` +
  `svc.Redis` auto-wiring.
