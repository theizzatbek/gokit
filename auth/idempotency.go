package auth

import (
	"context"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// IdempotencyHeader is the request header the middleware looks at. The
// constant matches what Stripe / RFC-draft "idempotency-key" use; for
// clients that prefer a vendor-specific name, set Idempotency-Key on
// every request and let your edge proxy normalise.
const IdempotencyHeader = "Idempotency-Key"

// IdempotencyReplayHeader is set on cached-replay responses so clients
// can distinguish "fresh execution" from "served from cache".
const IdempotencyReplayHeader = "X-Idempotency-Replay"

// CachedResponse is what gets stored per (method, path, key) tuple and
// replayed on a hit. Set-Cookie and Authorization are NOT carried over
// — they're session-specific and replaying them across distinct callers
// is a security mistake.
type CachedResponse struct {
	Status      int
	ContentType string
	Body        []byte
	Headers     map[string]string // additional headers worth preserving (Location, X-Request-ID, …)
}

// IdempotencyStore is the pluggable persistence layer. The default
// in-memory implementation (NewMemIdempotencyStore) is suitable for a
// single instance; for multi-replica services, wire a Redis-backed
// store that satisfies the same interface.
type IdempotencyStore interface {
	Get(ctx context.Context, key string) (*CachedResponse, bool)
	Set(ctx context.Context, key string, resp *CachedResponse, ttl time.Duration)
}

// Idempotency returns a Fiber middleware that dedupes write-method
// requests by Idempotency-Key. The first call runs the handler and the
// response is stored for ttl; subsequent calls with the same
// (method, path, Idempotency-Key) replay the cached response without
// invoking the handler.
//
//	app.Post("/orders", auth.Idempotency(24 * time.Hour), placeOrder)
//
// Behaviour:
//
//   - Requests with no Idempotency-Key header pass through untouched.
//   - Safe methods (GET, HEAD, OPTIONS) pass through untouched —
//     they're already idempotent.
//   - Handler errors (returned from c.Next()) propagate without
//     caching, so a transient failure doesn't poison the key.
//   - 5xx responses are NOT cached. A retry after a server bug can
//     succeed; only 2xx, 3xx, and 4xx outcomes are stable enough to
//     replay.
//   - On hit: the cached status / body / Content-Type / extra headers
//     are written verbatim plus X-Idempotency-Replay: true.
//
// The default store is in-memory, sync.Map-backed with lazy expiry.
// For multi-instance deployments, pass an external store via
// [IdempotencyWithStore].
func Idempotency(ttl time.Duration) fiber.Handler {
	return IdempotencyWithStore(ttl, NewMemIdempotencyStore())
}

// IdempotencyWithStore is the explicit-store variant of [Idempotency].
// Use when the in-memory default isn't enough — typically when the
// service runs multiple replicas behind a load balancer and two
// retries can hit different pods.
func IdempotencyWithStore(ttl time.Duration, store IdempotencyStore) fiber.Handler {
	return idempotencyHandler(ttl, store, nil)
}

// idempotencyHandler is the shared implementation. m is the optional
// authMetrics instance for outcome counting (nil-safe).
func idempotencyHandler(ttl time.Duration, store IdempotencyStore, m *authMetrics) fiber.Handler {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if store == nil {
		store = NewMemIdempotencyStore()
	}
	return func(c *fiber.Ctx) error {
		key := c.Get(IdempotencyHeader)
		if key == "" || !methodApplies(c.Method()) {
			m.incIdempotency("skip")
			return c.Next()
		}
		cacheKey := c.Method() + ":" + c.Path() + ":" + key

		if cached, ok := store.Get(c.UserContext(), cacheKey); ok {
			m.incIdempotency("hit")
			if cached.ContentType != "" {
				c.Set(fiber.HeaderContentType, cached.ContentType)
			}
			for k, v := range cached.Headers {
				c.Set(k, v)
			}
			c.Set(IdempotencyReplayHeader, "true")
			return c.Status(cached.Status).Send(cached.Body)
		}

		if err := c.Next(); err != nil {
			return err
		}
		status := c.Response().StatusCode()
		if status >= 500 {
			return nil // transient — let the retry get a fresh attempt
		}
		stored := &CachedResponse{
			Status:      status,
			ContentType: string(c.Response().Header.ContentType()),
			Body:        append([]byte(nil), c.Response().Body()...),
			Headers:     captureReplayHeaders(c),
		}
		store.Set(c.UserContext(), cacheKey, stored, ttl)
		m.incIdempotency("miss")
		return nil
	}
}

// Idempotency (method form) is the *Auth[C]-bound variant of the
// package-level [Idempotency]. Identical behaviour plus
// `auth_idempotency_total{outcome}` counters when [WithMetrics] is
// wired. Prefer this over the package-level form when scraping.
func (a *Auth[C]) Idempotency(ttl time.Duration) fiber.Handler {
	return idempotencyHandler(ttl, NewMemIdempotencyStore(), a.metrics)
}

// IdempotencyWithStore (method form) is the explicit-store variant of
// the *Auth[C]-bound Idempotency. Use with a Redis-backed store for
// multi-replica deployments; outcomes still feed
// `auth_idempotency_total` when WithMetrics is wired.
func (a *Auth[C]) IdempotencyWithStore(ttl time.Duration, store IdempotencyStore) fiber.Handler {
	return idempotencyHandler(ttl, store, a.metrics)
}

// methodApplies returns true for the write methods the middleware
// dedupes. Safe methods (GET/HEAD/OPTIONS) are idempotent by spec and
// skipped — running the handler twice has no observable effect.
func methodApplies(method string) bool {
	switch method {
	case fiber.MethodPost, fiber.MethodPut, fiber.MethodPatch, fiber.MethodDelete:
		return true
	}
	return false
}

// captureReplayHeaders snapshots the response headers worth replaying.
// Set-Cookie, Authorization-related headers, and CORS-internal headers
// are intentionally omitted — replaying them across different callers
// holding the same key would be a session-isolation mistake.
func captureReplayHeaders(c *fiber.Ctx) map[string]string {
	want := []string{
		fiber.HeaderLocation,
		"X-Request-ID",
		"ETag",
		"Last-Modified",
		"Retry-After",
	}
	out := map[string]string{}
	for _, h := range want {
		if v := c.GetRespHeader(h); v != "" {
			out[h] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// IdempotencyFactory adapts Idempotency to fibermap's middleware-factory
// signature. The argument is the TTL in Go-duration form (`"10m"`,
// `"24h"`, …) supplied via YAML:
//
//	middleware:
//	  - idempotency: ["24h"]
//
// Stores share state per factory invocation, so every route mounting
// `idempotency: ["..."]` gets its own in-memory cache. For shared
// state across routes, register a custom factory that closes over one
// IdempotencyStore.
func IdempotencyFactory(args []any) (fiber.Handler, error) {
	ttl, err := parseIdempotencyArgs(args)
	if err != nil {
		return nil, err
	}
	return Idempotency(ttl), nil
}

// IdempotencyFactory (method form) is the *Auth[C]-bound variant of
// the package-level [IdempotencyFactory]. fibermount registers this
// variant for you so YAML-mounted `idempotency` chains feed
// `auth_idempotency_total` when WithMetrics is wired.
func (a *Auth[C]) IdempotencyFactory(args []any) (fiber.Handler, error) {
	ttl, err := parseIdempotencyArgs(args)
	if err != nil {
		return nil, err
	}
	return a.Idempotency(ttl), nil
}

func parseIdempotencyArgs(args []any) (time.Duration, error) {
	if len(args) != 1 {
		return 0, xerrs.Internalf(CodeInvalidFactoryArgs,
			"idempotency: expected [ttl], got %d args", len(args))
	}
	ttlStr, ok := args[0].(string)
	if !ok {
		return 0, xerrs.Internalf(CodeInvalidFactoryArgs,
			"idempotency: ttl must be a duration string, got %T", args[0])
	}
	ttl, err := time.ParseDuration(ttlStr)
	if err != nil {
		return 0, xerrs.Internalf(CodeInvalidFactoryArgs,
			"idempotency: ttl %q is not a valid duration: %v", ttlStr, err)
	}
	return ttl, nil
}

// memIdempotencyStore is the default in-process IdempotencyStore.
// Lazy-expires on Get; no eviction beyond that. For huge unbounded key
// sets, wire a Redis-backed alternative.
type memIdempotencyStore struct {
	mu    sync.Mutex
	items map[string]memIdempotencyEntry
}

type memIdempotencyEntry struct {
	resp    *CachedResponse
	expires time.Time
}

// NewMemIdempotencyStore returns an in-memory store suitable for
// single-instance deployments. Concurrency-safe; lazy expiry.
func NewMemIdempotencyStore() IdempotencyStore {
	return &memIdempotencyStore{items: map[string]memIdempotencyEntry{}}
}

func (s *memIdempotencyStore) Get(_ context.Context, key string) (*CachedResponse, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.items[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		delete(s.items, key)
		return nil, false
	}
	return e.resp, true
}

func (s *memIdempotencyStore) Set(_ context.Context, key string, resp *CachedResponse, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[key] = memIdempotencyEntry{resp: resp, expires: time.Now().Add(ttl)}
}
