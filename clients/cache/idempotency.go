package cache

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	redisclient "github.com/theizzatbek/gokit/clients/redis"
	"github.com/theizzatbek/gokit/fibermap"
)

// RedisIdempotencyStore is the Redis-backed implementation of
// [fibermap.IdempotencyStore]. Plug into [fibermap.IdempotencyKey]:
//
//	store := cache.NewIdempotencyStore(svc.Redis, "idem:payments:")
//	app.Post("/payments", fibermap.IdempotencyKey(store), handler)
//
// Nil-safe: a nil receiver from a nil *redisclient.Client returns
// nil; the middleware then operates as if no store is wired and
// every request passes through uncached — useful for local dev
// without Redis.
//
// Errors are best-effort under the
// [fibermap.IdempotencyStore] contract: a Redis transport failure
// on Get is logged + reported as "miss" (the source-of-truth handler
// still runs); Set transport errors are logged + suppressed (the
// foreground request still completes). Encode / decode failures
// likewise. This keeps the middleware Liveness-friendly: a Redis
// blip can NEVER turn a write into a 500.
type RedisIdempotencyStore struct {
	rdb    redis.UniversalClient
	prefix string
	logger *slog.Logger
}

// NewIdempotencyStore returns a store backed by the supplied Redis
// client (typically `svc.Redis`). prefix is required and panics
// when empty — a shared Redis serving multiple services would
// collide on identical keys without it.
//
// Returns nil when rc is nil so callers can write
// `fibermap.IdempotencyKey(cache.NewIdempotencyStore(svc.Redis, ...))`
// unconditionally; the middleware checks for nil store internally
// (see [fibermap.IdempotencyKey] semantics for nil stores) and
// degrades gracefully.
func NewIdempotencyStore(rc *redisclient.Client, prefix string) *RedisIdempotencyStore {
	if rc == nil {
		return nil
	}
	if prefix == "" {
		panic("cache.NewIdempotencyStore: prefix is required to avoid key collisions on shared Redis")
	}
	return &RedisIdempotencyStore{
		rdb:    rc.Universal(),
		prefix: prefix,
		logger: rc.Logger(),
	}
}

// Get retrieves the captured response under key. Nil-receiver / nil
// underlying client returns (nil, nil) so the middleware treats it
// as a miss and runs the handler. Transport / decode errors are
// also reported as a miss (logged at Warn).
func (s *RedisIdempotencyStore) Get(ctx context.Context, key string) (*fibermap.StoredResponse, error) {
	if s == nil || s.rdb == nil {
		return nil, nil
	}
	raw, err := s.rdb.Get(ctx, s.prefix+key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		if s.logger != nil {
			s.logger.Warn("idempotency: redis get failed", "key", key, "err", err.Error())
		}
		return nil, nil
	}
	var resp fibermap.StoredResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		if s.logger != nil {
			s.logger.Warn("idempotency: decode failed", "key", key, "err", err.Error())
		}
		return nil, nil
	}
	return &resp, nil
}

// Set persists resp under key with the supplied TTL. Encode +
// transport failures are logged and swallowed — the foreground
// request must not depend on a successful cache write.
func (s *RedisIdempotencyStore) Set(ctx context.Context, key string, resp *fibermap.StoredResponse, ttl time.Duration) error {
	if s == nil || s.rdb == nil {
		return nil
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("idempotency: encode failed", "key", key, "err", err.Error())
		}
		return nil
	}
	if err := s.rdb.Set(ctx, s.prefix+key, raw, ttl).Err(); err != nil {
		if s.logger != nil {
			s.logger.Warn("idempotency: redis set failed", "key", key, "err", err.Error())
		}
	}
	return nil
}

// lockSuffix is appended to the cached-response key to form the
// concurrency-lock key. Namespacing the lock under a sibling key
// (instead of overloading the response key with a sentinel) avoids
// a race between SETNX-lock and the Set-response write that would
// otherwise need a Lua script to keep atomic.
const lockSuffix = ":lock"

// AcquireLock attempts to claim a short-lived concurrency lock for
// the in-flight request keyed by `key`. Returns (true, nil) on
// successful claim, (false, nil) when another request holds the
// lock, (true, err) on transport failure (fail open — kit
// middleware contract).
//
// Implements [fibermap.IdempotencyLocker] — the middleware
// auto-detects the interface and engages the lock-path when
// present.
func (s *RedisIdempotencyStore) AcquireLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if s == nil || s.rdb == nil {
		return true, nil
	}
	ok, err := s.rdb.SetNX(ctx, s.prefix+key+lockSuffix, "1", ttl).Result()
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("idempotency: redis setnx failed", "key", key, "err", err.Error())
		}
		// Fail open — never deny a write because of a Redis blip.
		return true, err
	}
	return ok, nil
}

// ReleaseLock removes the concurrency lock previously claimed by
// AcquireLock. Best-effort: failures are logged + swallowed. Stays
// nil-safe (called from a deferred closure in the middleware).
func (s *RedisIdempotencyStore) ReleaseLock(ctx context.Context, key string) error {
	if s == nil || s.rdb == nil {
		return nil
	}
	if err := s.rdb.Del(ctx, s.prefix+key+lockSuffix).Err(); err != nil {
		if s.logger != nil {
			s.logger.Warn("idempotency: redis del lock failed", "key", key, "err", err.Error())
		}
	}
	return nil
}
