// Package cache is a typed Redis-backed read-through cache. The
// generic Redis[T] handles encode/decode, positive/negative caching,
// prefix namespacing, and best-effort error handling so callers
// never have to defend against transient Redis failures — a cache
// miss is always a recoverable signal.
//
// Construction takes a redis.UniversalClient (typically from
// clients/redis.Client.Universal()). The cache doesn't own the pool;
// the caller closes it.
//
//	links, _ := cache.New[CachedLink](svc.Redis.Universal(), cache.Config{
//	    KeyPrefix:   "urlshort:link:",
//	    PositiveTTL: time.Hour,
//	    NegativeTTL: time.Minute,
//	    Logger:      svc.Logger(),
//	})
//
//	hit := links.Get(ctx, "ab12")
//	switch {
//	case hit.Value != nil:           // positive hit
//	case hit.NotFound:                // negative hit
//	default:                          // miss → fall through to source
//	}
//
// Errors from Redis are logged + swallowed: a transport blip yields
// a miss, not an error returned up the stack. The source of truth
// stays authoritative.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	mathrand "math/rand/v2"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	redisclient "github.com/theizzatbek/gokit/clients/redis"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Stable error Codes. Operational failures (Redis transport) are
// never wrapped in *errs.Error — they hit the logger only.
const (
	// CodeInvalidKeyPrefix — Config.KeyPrefix was empty at New time.
	CodeInvalidKeyPrefix = "cache_invalid_key_prefix"
	// CodeInvalidConfig — Config has another validation failure
	// (e.g. MetricsReg set without Name).
	CodeInvalidConfig = "cache_invalid_config"
)

// negSentinel is the value stored under a key to mark it as
// "known not found". A single NUL byte cannot collide with any
// JSON-encoded payload (JSON is ASCII-text at the top level).
const negSentinel = "\x00"

// Codec serialises values to/from the bytes stored in Redis. JSON is
// the default; pass via Config.Codec to swap (msgpack, protobuf, …).
type Codec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(b []byte, v any) error
}

// JSONCodec is the default codec used when Config.Codec is nil.
type JSONCodec struct{}

func (JSONCodec) Marshal(v any) ([]byte, error)   { return json.Marshal(v) }
func (JSONCodec) Unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// Config configures [New]. KeyPrefix is the only required field;
// every other zero value gets a documented default.
type Config struct {
	// KeyPrefix is prepended to every Redis key. Required —
	// shared Redis instances need namespace isolation between
	// services AND between value types within one service. Pass
	// e.g. "urlshort:link:" or "session:".
	KeyPrefix string

	// PositiveTTL bounds the age of a hit. Default 1h.
	PositiveTTL time.Duration

	// NegativeTTL bounds the age of a "key not found" sentinel.
	// Default 60s. Set to 0 to disable negative caching entirely.
	NegativeTTL time.Duration

	// Logger receives Warn-level entries on Redis errors. nil
	// silently swallows them.
	Logger *slog.Logger

	// Name labels this cache instance in metrics. Required when
	// [Config.MetricsReg] is wired. Bounded cardinality — pick
	// stable per-cache identifiers (e.g. "links", "users").
	Name string

	// MetricsReg, when non-nil, registers cache_operations_total +
	// cache_operation_duration_seconds. Requires Name.
	MetricsReg prometheus.Registerer

	// TTLJitter is the fraction of uniform noise applied to the
	// PositiveTTL / NegativeTTL on every Set call. 0 disables (the
	// default). Typical: 0.10 - 0.25. Defends popular keys against
	// synchronised expiry storms.
	TTLJitter float64

	// Codec overrides the default JSON serialisation. Pass nil to
	// keep JSON.
	Codec Codec
}

// Lookup is the tri-state result returned by [Redis.Get]:
//
//   - Value != nil, NotFound == false → positive hit. Use Value.
//   - Value == nil, NotFound == true  → negative hit ("known bad").
//     Treat as not-found without falling through to the source.
//   - Value == nil, NotFound == false → miss. Query the source.
type Lookup[T any] struct {
	Value    *T
	NotFound bool
}

// Redis is the typed cache handle. Construct via [New]; close the
// underlying Redis client externally.
//
// A nil *Redis[T] receiver is safe on every method — Get returns a
// miss, the writers are no-ops. Lets callers thread a cache
// reference through their code unconditionally; cache-off is a
// matter of not constructing one.
//
// The handle wraps a redis.UniversalClient so the same kit type
// works against single-node, cluster, and sentinel deployments.
type Redis[T any] struct {
	rdb       redis.UniversalClient
	prefix    string
	posTTL    time.Duration
	negTTL    time.Duration
	logger    *slog.Logger
	negEnable bool
	jitter    float64
	codec     Codec
	metrics   *metrics
	sf        singleflight.Group
}

// New wraps rdb as a typed cache. Validates KeyPrefix; everything
// else falls back to defaults documented on [Config].
//
// rdb is a redis.UniversalClient — pass any of *redis.Client /
// *redis.ClusterClient / *redis.FailoverClient. Single-mode callers
// using clients/redis can supply rc.Universal() (or rc.Redis() which
// satisfies the interface).
func New[T any](rdb redis.UniversalClient, cfg Config) (*Redis[T], error) {
	if cfg.KeyPrefix == "" {
		return nil, xerrs.Validation(CodeInvalidKeyPrefix,
			"cache: Config.KeyPrefix is required")
	}
	if cfg.PositiveTTL <= 0 {
		cfg.PositiveTTL = time.Hour
	}
	negEnable := cfg.NegativeTTL > 0
	if cfg.NegativeTTL == 0 {
		cfg.NegativeTTL = 60 * time.Second
		negEnable = true
	}
	if cfg.TTLJitter < 0 {
		cfg.TTLJitter = 0
	}
	if cfg.TTLJitter > 1 {
		cfg.TTLJitter = 1
	}
	codec := cfg.Codec
	if codec == nil {
		codec = JSONCodec{}
	}
	var m *metrics
	if cfg.MetricsReg != nil {
		if cfg.Name == "" {
			return nil, xerrs.Validation(CodeInvalidConfig,
				"cache: Config.Name is required when Config.MetricsReg is set")
		}
		m = newMetrics(cfg.MetricsReg, cfg.Name)
	}
	return &Redis[T]{
		rdb:       rdb,
		prefix:    cfg.KeyPrefix,
		posTTL:    cfg.PositiveTTL,
		negTTL:    cfg.NegativeTTL,
		logger:    cfg.Logger,
		negEnable: negEnable,
		jitter:    cfg.TTLJitter,
		codec:     codec,
		metrics:   m,
	}, nil
}

// For is the kit-aware convenience constructor: takes a
// [*redisclient.Client] (rather than the raw client), auto-wires its
// logger, applies default TTLs, and threads through
// rc.Universal() so cluster / sentinel deployments just work.
// Returns nil when rc is nil — call sites can wire the cache
// unconditionally and rely on the nil-receiver-safe API.
//
// Panics with *errs.Error{Code: CodeInvalidKeyPrefix} on empty
// keyPrefix — same "panic-on-misuse" convention as
// fibermap.RegisterHandler.
//
// For full control (custom TTLs, codec, metrics, jitter), fall back
// to [New].
func For[T any](rc *redisclient.Client, keyPrefix string) *Redis[T] {
	if rc == nil {
		return nil
	}
	c, err := New[T](rc.Universal(), Config{
		KeyPrefix: keyPrefix,
		Logger:    rc.Logger(),
	})
	if err != nil {
		panic(err)
	}
	return c
}

// Get reads from Redis. Returns a miss on transport error so callers
// can always fall through; the Logger surfaces the actual cause.
func (c *Redis[T]) Get(ctx context.Context, key string) Lookup[T] {
	if c == nil {
		return Lookup[T]{}
	}
	start := time.Now()
	raw, err := c.rdb.Get(ctx, c.prefix+key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			c.metrics.observe("get", "miss", time.Since(start))
			return Lookup[T]{}
		}
		if c.logger != nil {
			c.logger.Warn("cache: get failed", "key", key, "err", err.Error())
		}
		c.metrics.observe("get", "error", time.Since(start))
		return Lookup[T]{}
	}
	if c.negEnable && raw == negSentinel {
		c.metrics.observe("get", "negative", time.Since(start))
		return Lookup[T]{NotFound: true}
	}
	var v T
	if err := c.codec.Unmarshal([]byte(raw), &v); err != nil {
		if c.logger != nil {
			c.logger.Warn("cache: decode failed", "key", key, "err", err.Error())
		}
		c.metrics.observe("get", "error", time.Since(start))
		return Lookup[T]{}
	}
	c.metrics.observe("get", "hit", time.Since(start))
	return Lookup[T]{Value: &v}
}

// Set stores a positive entry. Encode / transport errors are logged
// and swallowed — the source of truth still has the data.
func (c *Redis[T]) Set(ctx context.Context, key string, value T) {
	if c == nil {
		return
	}
	start := time.Now()
	raw, err := c.codec.Marshal(value)
	if err != nil {
		if c.logger != nil {
			c.logger.Warn("cache: encode failed", "key", key, "err", err.Error())
		}
		c.metrics.observe("set", "error", time.Since(start))
		return
	}
	ttl := applyJitter(c.posTTL, c.jitter)
	if err := c.rdb.Set(ctx, c.prefix+key, raw, ttl).Err(); err != nil {
		if c.logger != nil {
			c.logger.Warn("cache: set failed", "key", key, "err", err.Error())
		}
		c.metrics.observe("set", "error", time.Since(start))
		return
	}
	c.metrics.observe("set", "ok", time.Since(start))
}

// SetNotFound stores the negative-cache sentinel for key with
// NegativeTTL. No-op when negative caching is disabled.
func (c *Redis[T]) SetNotFound(ctx context.Context, key string) {
	if c == nil || !c.negEnable {
		return
	}
	start := time.Now()
	ttl := applyJitter(c.negTTL, c.jitter)
	if err := c.rdb.Set(ctx, c.prefix+key, negSentinel, ttl).Err(); err != nil {
		if c.logger != nil {
			c.logger.Warn("cache: set not-found failed", "key", key, "err", err.Error())
		}
		c.metrics.observe("set_not_found", "error", time.Since(start))
		return
	}
	c.metrics.observe("set_not_found", "ok", time.Since(start))
}

// Invalidate drops the entry (positive or negative) under key.
// Called after a write to the source of truth so the next Get
// refetches.
func (c *Redis[T]) Invalidate(ctx context.Context, key string) {
	if c == nil {
		return
	}
	start := time.Now()
	if err := c.rdb.Del(ctx, c.prefix+key).Err(); err != nil {
		if c.logger != nil {
			c.logger.Warn("cache: invalidate failed", "key", key, "err", err.Error())
		}
		c.metrics.observe("invalidate", "error", time.Since(start))
		return
	}
	c.metrics.observe("invalidate", "ok", time.Since(start))
}

// InvalidatePrefix drops every key whose suffix starts with `partial`
// under the configured KeyPrefix. Walks the keyspace via SCAN +
// pipelined DEL — bounded round-trips, no KEYS (which blocks). Use
// for "drop every entry of this tenant" flows; the singular
// Invalidate stays the right choice for one-key scope.
//
// Best-effort under the kit policy — scan/delete errors are logged
// + swallowed.
//
// Cluster mode caveat: SCAN runs per-shard. The kit issues a single
// SCAN here; for full-coverage cluster cleanup invoke
// rdb.ForEachShard or use a hashtag-pinned key layout so all entries
// land on one shard.
func (c *Redis[T]) InvalidatePrefix(ctx context.Context, partial string) {
	if c == nil {
		return
	}
	start := time.Now()
	pattern := c.prefix + partial + "*"
	var cursor uint64
	for {
		keys, next, err := c.rdb.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			if c.logger != nil {
				c.logger.Warn("cache: invalidate prefix scan failed",
					"prefix", partial, "err", err.Error())
			}
			c.metrics.observe("invalidate_prefix", "error", time.Since(start))
			return
		}
		if len(keys) > 0 {
			if err := c.rdb.Del(ctx, keys...).Err(); err != nil && c.logger != nil {
				c.logger.Warn("cache: invalidate prefix del failed",
					"prefix", partial, "err", err.Error())
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	c.metrics.observe("invalidate_prefix", "ok", time.Since(start))
}

// LoaderFn is the loader signature passed to [Redis.GetOrLoad].
// Return (value, true, nil) on a hit at the source, (zero, false,
// nil) on a source-level miss (which the kit caches negatively),
// and (zero, false, err) on a transport / decode failure (the kit
// surfaces it to the caller and does NOT poison the cache).
type LoaderFn[T any] func(ctx context.Context, key string) (T, bool, error)

// GetOrLoad is the read-through helper. Resolves key against the
// cache; on miss calls loader, populates the cache (positive or
// negative), and returns the Lookup. Single-flight protects against
// stampede — concurrent calls for the same key fold into one loader
// invocation, then split out with identical Lookup results.
//
// Loader errors are NOT cached; the request returns (Lookup{}, err)
// and the next call retries. Loader (zero, false, nil) populates a
// negative entry; subsequent Lookups return NotFound until the
// negative TTL expires.
//
// Honours the same nil-receiver and best-effort error semantics as
// the rest of the API: a nil *Redis[T] just calls loader directly.
func (c *Redis[T]) GetOrLoad(ctx context.Context, key string, loader LoaderFn[T]) (Lookup[T], error) {
	if c == nil {
		if loader == nil {
			return Lookup[T]{}, nil
		}
		val, found, err := loader(ctx, key)
		if err != nil {
			return Lookup[T]{}, err
		}
		if !found {
			return Lookup[T]{NotFound: true}, nil
		}
		v := val
		return Lookup[T]{Value: &v}, nil
	}
	if hit := c.Get(ctx, key); hit.Value != nil || hit.NotFound {
		return hit, nil
	}
	if loader == nil {
		return Lookup[T]{}, nil
	}
	type result struct {
		hit Lookup[T]
		err error
	}
	v, _, _ := c.sf.Do(key, func() (any, error) {
		// Re-check after acquiring the flight slot — another
		// goroutine may have populated the cache while we waited.
		if hit := c.Get(ctx, key); hit.Value != nil || hit.NotFound {
			return result{hit: hit}, nil
		}
		val, found, lerr := loader(ctx, key)
		if lerr != nil {
			return result{err: lerr}, nil
		}
		if !found {
			c.SetNotFound(ctx, key)
			return result{hit: Lookup[T]{NotFound: true}}, nil
		}
		c.Set(ctx, key, val)
		copy := val
		return result{hit: Lookup[T]{Value: &copy}}, nil
	})
	r := v.(result)
	return r.hit, r.err
}

// applyJitter perturbs d by ±jitter (fraction) of d. jitter == 0
// short-circuits. Uses the top-level math/rand/v2 — thread-safe via
// per-CPU state.
func applyJitter(d time.Duration, jitter float64) time.Duration {
	if jitter <= 0 || d <= 0 {
		return d
	}
	delta := float64(d) * jitter
	noise := (mathrand.Float64()*2 - 1) * delta
	out := d + time.Duration(noise)
	if out <= 0 {
		return d
	}
	return out
}
