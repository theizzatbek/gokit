# auth/refreshredis

Redis-backed `auth.RefreshStore` over `redis/go-redis/v9`. Each record is one HASH with `EXPIREAT`; family + subject SETs back the bulk-revoke paths. `Consume` runs as a single Lua script for atomicity (consume + reuse detection + family revoke all server-side).

**Parent:** [../README.md](../README.md)
**Import:** `github.com/theizzatbek/gokit/auth/refreshredis`

## Use

```go
import (
    "github.com/redis/go-redis/v9"
    "github.com/theizzatbek/gokit/auth"
    "github.com/theizzatbek/gokit/auth/refreshredis"
)

rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

authObj, _ := auth.New[MyClaims](auth.Config{
    Issuer: "myservice", Keys: ks, AccessTTL: 15*time.Minute, RefreshTTL: 30*24*time.Hour,
}, auth.WithRefreshStore(refreshredis.New(rdb)))
```

## Notes

- **Same contract as [`refreshpg`](../refreshpg/README.md).** Picks Redis when you'd rather not extend your Postgres schema, or want sub-millisecond Consume.
- **Auto-expiration via `EXPIREAT`.** Expired tokens GC themselves — no periodic cleanup job needed.
- **Single Lua script per Consume.** Eliminates the round-trip race window between SELECT + UPDATE. Replicates correctly via Redis MULTI semantics.
- **Family + subject SETs** index records for `RevokeFamily` and `RevokeAllForSubject` without scanning. SETs auto-EXPIRE alongside the longest-lived family member.
- **Token hashes only.** Same security property as refreshpg — DB/cache leak doesn't compromise raw tokens.
- **Cluster-safe** when keys for a given family hash to the same slot — Redis handles this via hash-tags built into the key naming scheme.

## Choosing between refreshpg and refreshredis

| Concern | refreshpg | refreshredis |
|---|---|---|
| Single source of truth | ✓ (in same DB as users) | ✗ (separate persistence) |
| Sub-ms Consume latency | medium | ✓ |
| Auto-expiration | ✗ (manual cleanup) | ✓ |
| Failure-domain | shared with app DB | independent |
| Operational overhead | none extra | Redis instance |

Most services start with `refreshpg` and migrate to `refreshredis` only if refresh latency becomes a hotspot or they're separating short-lived state from durable data.

## Testing

Use [testcontainers-go/modules/redis](https://golang.testcontainers.org/modules/redis/):

```go
c, _ := tcredis.Run(ctx, "redis:7-alpine")
defer testcontainers.TerminateContainer(c)

endpoint, _ := c.Endpoint(ctx, "")
rdb := redis.NewClient(&redis.Options{Addr: endpoint})
store := refreshredis.New(rdb)
```

## See also

- [`auth`](../README.md) — parent
- [`auth/refreshpg`](../refreshpg/README.md) — Postgres-backed alternative with identical contract
