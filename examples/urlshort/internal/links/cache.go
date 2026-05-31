package links

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// CachedLink is the trimmed-down projection of Link that's safe to
// cache. visit_count + last_visited_at deliberately omitted — they
// change on every click and would require an invalidation for each,
// defeating the cache's purpose.
type CachedLink struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Code        string `json:"code"`
	OriginalURL string `json:"original_url"`
}

// LinkCache is the Redis-backed cache layer in front of the links
// table. Hot path for GET /:code — a cache hit means no Postgres
// round-trip at all. Negative caching (NotFound for an unknown code)
// uses a shorter TTL to absorb scanner traffic without filling Redis
// with 404 entries forever.
//
// A nil *LinkCache is a no-op pass-through: every Get returns
// (nil, false, nil) and Set/Invalidate silently succeed. Lets the
// service code call cache.Get unconditionally even when Redis is
// disabled in dev.
type LinkCache struct {
	rdb            *redis.Client
	posTTL         time.Duration
	negTTL         time.Duration
	log            *slog.Logger
	keyPrefix      string
	negSentinel    string // value stored at negative-cache keys to distinguish from a missing key
	negSentinelLen int
}

// LinkCacheConfig configures NewLinkCache. Zero values get sensible
// production defaults.
type LinkCacheConfig struct {
	PositiveTTL time.Duration // default 1h — code → URL mapping is mostly immutable
	NegativeTTL time.Duration // default 60s — bounded so the cache doesn't poison after a Create
	KeyPrefix   string        // default "urlshort:link:"
	Logger      *slog.Logger
}

// NewLinkCache wraps an existing *redis.Client. The caller owns the
// client's lifetime; cache.Close is intentionally absent.
func NewLinkCache(rdb *redis.Client, cfg LinkCacheConfig) *LinkCache {
	if cfg.PositiveTTL <= 0 {
		cfg.PositiveTTL = time.Hour
	}
	if cfg.NegativeTTL <= 0 {
		cfg.NegativeTTL = 60 * time.Second
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "urlshort:link:"
	}
	const sentinel = "\x00404"
	return &LinkCache{
		rdb:            rdb,
		posTTL:         cfg.PositiveTTL,
		negTTL:         cfg.NegativeTTL,
		log:            cfg.Logger,
		keyPrefix:      cfg.KeyPrefix,
		negSentinel:    sentinel,
		negSentinelLen: len(sentinel),
	}
}

// LookupResult disambiguates the three Get outcomes without forcing
// callers into a tri-bool: (link, hit, miss, notFound).
type LookupResult struct {
	Link     *CachedLink // non-nil only on a positive hit
	NotFound bool        // true on a negative-cache hit (code known-bad)
}

// Get returns the cached link projection for code.
//   - (Link != nil, NotFound = false): positive hit, use Link.
//   - (nil, true): negative hit, the code is known-bad; return 404 without DB.
//   - (nil, false): miss, fall through to Postgres.
//
// Redis errors are NEVER propagated — cache is best-effort. The
// caller sees a miss and queries the source of truth.
func (c *LinkCache) Get(ctx context.Context, code string) LookupResult {
	if c == nil {
		return LookupResult{}
	}
	raw, err := c.rdb.Get(ctx, c.keyPrefix+code).Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) && c.log != nil {
			c.log.Warn("urlshort cache: get failed", "code", code, "err", err.Error())
		}
		return LookupResult{}
	}
	if raw == c.negSentinel {
		return LookupResult{NotFound: true}
	}
	var l CachedLink
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		if c.log != nil {
			c.log.Warn("urlshort cache: decode failed", "code", code, "err", err.Error())
		}
		return LookupResult{}
	}
	return LookupResult{Link: &l}
}

// Set stores a positive entry for the link's code. Encoding failures
// are logged and ignored — cache misses are always recoverable.
func (c *LinkCache) Set(ctx context.Context, l CachedLink) {
	if c == nil {
		return
	}
	raw, err := json.Marshal(l)
	if err != nil {
		if c.log != nil {
			c.log.Warn("urlshort cache: encode failed", "code", l.Code, "err", err.Error())
		}
		return
	}
	if err := c.rdb.Set(ctx, c.keyPrefix+l.Code, raw, c.posTTL).Err(); err != nil && c.log != nil {
		c.log.Warn("urlshort cache: set failed", "code", l.Code, "err", err.Error())
	}
}

// SetNotFound stores the negative-cache sentinel under code so the
// next lookup short-circuits without a DB call. TTL is the
// configured NegativeTTL — keep it modest so a later Create takes
// effect within the window.
func (c *LinkCache) SetNotFound(ctx context.Context, code string) {
	if c == nil {
		return
	}
	if err := c.rdb.Set(ctx, c.keyPrefix+code, c.negSentinel, c.negTTL).Err(); err != nil && c.log != nil {
		c.log.Warn("urlshort cache: set 404 failed", "code", code, "err", err.Error())
	}
}

// Invalidate removes any cached entry (positive or negative) for
// code. Called by Update / Delete after the DB write succeeds so the
// next Resolve refetches.
func (c *LinkCache) Invalidate(ctx context.Context, code string) {
	if c == nil {
		return
	}
	if err := c.rdb.Del(ctx, c.keyPrefix+code).Err(); err != nil && c.log != nil {
		c.log.Warn("urlshort cache: invalidate failed", "code", code, "err", err.Error())
	}
}
