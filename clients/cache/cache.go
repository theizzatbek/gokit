// Package cache is a typed Redis-backed read-through cache. The
// generic Redis[T] handles encode/decode, positive/negative caching,
// prefix namespacing, and best-effort error handling so callers
// never have to defend against transient Redis failures — a cache
// miss is always a recoverable signal.
//
// Construction takes a raw *redis.Client (typically from
// clients/redis.Client.Redis()). The cache doesn't own the pool;
// the caller closes it.
//
//	rdb := svc.Redis.Redis()
//	links, _ := cache.New[CachedLink](rdb, cache.Config{
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
	"time"

	"github.com/redis/go-redis/v9"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Stable error Codes. Operational failures (Redis transport) are
// never wrapped in *errs.Error — they hit the logger only.
const (
	// CodeInvalidKeyPrefix — Config.KeyPrefix was empty at New time.
	CodeInvalidKeyPrefix = "cache_invalid_key_prefix"
)

// negSentinel is the value stored under a key to mark it as
// "known not found". A single NUL byte cannot collide with any
// JSON-encoded payload (JSON is ASCII-text at the top level).
const negSentinel = "\x00"

// Config configures [New]. KeyPrefix is the only required field;
// every other zero value gets a documented default.
type Config struct {
	// KeyPrefix is prepended to every Redis key. Required —
	// shared Redis instances need namespace isolation between
	// services AND between value types within one service. Pass
	// e.g. "urlshort:link:" or "session:".
	KeyPrefix string

	// PositiveTTL bounds the age of a hit. Default 1h. The cache
	// stores nothing forever — fall-through on miss is always
	// safe.
	PositiveTTL time.Duration

	// NegativeTTL bounds the age of a "key not found" sentinel.
	// Default 60s. Set to 0 to disable negative caching entirely
	// (every miss falls through to Redis-then-source on every
	// call).
	NegativeTTL time.Duration

	// Logger receives Warn-level entries on Redis errors. nil
	// silently swallows them.
	Logger *slog.Logger
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
// underlying *redis.Client externally.
//
// A nil *Redis[T] receiver is safe on every method — Get returns a
// miss, the writers are no-ops. Lets callers thread a cache
// reference through their code unconditionally; cache-off is a
// matter of not constructing one.
type Redis[T any] struct {
	rdb       *redis.Client
	prefix    string
	posTTL    time.Duration
	negTTL    time.Duration
	logger    *slog.Logger
	negEnable bool
}

// New wraps rdb as a typed cache. Validates KeyPrefix; everything
// else falls back to defaults documented on [Config].
func New[T any](rdb *redis.Client, cfg Config) (*Redis[T], error) {
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
	return &Redis[T]{
		rdb:       rdb,
		prefix:    cfg.KeyPrefix,
		posTTL:    cfg.PositiveTTL,
		negTTL:    cfg.NegativeTTL,
		logger:    cfg.Logger,
		negEnable: negEnable,
	}, nil
}

// Get reads from Redis. Returns a miss on transport error so callers
// can always fall through; the Logger surfaces the actual cause.
func (c *Redis[T]) Get(ctx context.Context, key string) Lookup[T] {
	if c == nil {
		return Lookup[T]{}
	}
	raw, err := c.rdb.Get(ctx, c.prefix+key).Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) && c.logger != nil {
			c.logger.Warn("cache: get failed", "key", key, "err", err.Error())
		}
		return Lookup[T]{}
	}
	if c.negEnable && raw == negSentinel {
		return Lookup[T]{NotFound: true}
	}
	var v T
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		if c.logger != nil {
			c.logger.Warn("cache: decode failed", "key", key, "err", err.Error())
		}
		return Lookup[T]{}
	}
	return Lookup[T]{Value: &v}
}

// Set stores a positive entry. Encode / transport errors are logged
// and swallowed — the source of truth still has the data.
func (c *Redis[T]) Set(ctx context.Context, key string, value T) {
	if c == nil {
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		if c.logger != nil {
			c.logger.Warn("cache: encode failed", "key", key, "err", err.Error())
		}
		return
	}
	if err := c.rdb.Set(ctx, c.prefix+key, raw, c.posTTL).Err(); err != nil && c.logger != nil {
		c.logger.Warn("cache: set failed", "key", key, "err", err.Error())
	}
}

// SetNotFound stores the negative-cache sentinel for key with
// NegativeTTL. No-op when negative caching is disabled (NegativeTTL
// explicitly set to a sentinel-disabling value via Config — see
// [New]) or when the receiver is nil.
func (c *Redis[T]) SetNotFound(ctx context.Context, key string) {
	if c == nil || !c.negEnable {
		return
	}
	if err := c.rdb.Set(ctx, c.prefix+key, negSentinel, c.negTTL).Err(); err != nil && c.logger != nil {
		c.logger.Warn("cache: set not-found failed", "key", key, "err", err.Error())
	}
}

// Invalidate drops the entry (positive or negative) under key.
// Called after a write to the source of truth so the next Get
// refetches.
func (c *Redis[T]) Invalidate(ctx context.Context, key string) {
	if c == nil {
		return
	}
	if err := c.rdb.Del(ctx, c.prefix+key).Err(); err != nil && c.logger != nil {
		c.logger.Warn("cache: invalidate failed", "key", key, "err", err.Error())
	}
}
