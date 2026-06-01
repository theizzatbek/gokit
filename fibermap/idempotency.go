package fibermap

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/errs"
)

// Stable error Code constants returned by [IdempotencyKey].
const (
	// CodeIdempotencyKeyMissing — handler enforced via
	// [WithIdempotencyRequired] but the inbound request did not
	// carry the configured header.
	CodeIdempotencyKeyMissing = "idempotency_key_missing"

	// CodeIdempotencyInFlight — another request with the same
	// idempotency-key is currently executing. The store implements
	// [IdempotencyLocker] and refused the SETNX-style lock. Caller
	// should retry after a short backoff. Mapped to 409 by
	// errs.HTTP.
	CodeIdempotencyInFlight = "idempotency_in_flight"
)

// StoredResponse is the captured shape replayed on subsequent hits
// with the same idempotency key. Marshalable by encoding/json so any
// kit-shaped cache (clients/cache.Redis[StoredResponse]) works.
//
// Only the body, status, and Content-Type are replayed — other
// response headers (Set-Cookie, X-Request-ID, custom domain
// headers) intentionally do NOT survive. Replaying Set-Cookie would
// hand a stale session to a different caller; X-Request-ID belongs
// to the new request not the original. Keep the stored shape
// minimal so the contract stays understandable.
type StoredResponse struct {
	Status      int    `json:"status"`
	Body        []byte `json:"body"`
	ContentType string `json:"content_type"`
}

// IdempotencyStore is the persistence backend [IdempotencyKey] uses
// to keep the captured response between the first and replay
// requests. Get returns nil on miss (NOT an error); Set replaces any
// prior entry. Errors are caller-defined — the middleware logs and
// continues on Get failure, logs on Set failure (the foreground
// request still completes).
//
// clients/cache supplies a default implementation backed by
// Redis[StoredResponse]. Roll your own for non-Redis backends
// (BadgerDB, ristretto, in-memory map for tests).
type IdempotencyStore interface {
	Get(ctx context.Context, key string) (*StoredResponse, error)
	Set(ctx context.Context, key string, resp *StoredResponse, ttl time.Duration) error
}

// IdempotencyLocker is an OPTIONAL extension to [IdempotencyStore]
// that lets the middleware hold a short-lived lock around the
// in-flight handler. When a store implements this interface (e.g.
// the Redis-backed cache.RedisIdempotencyStore via SETNX), the
// middleware:
//
//  1. Attempts AcquireLock(ctx, key, lockTTL) on cache miss.
//  2. If acquired == false, returns 409 with Code
//     CodeIdempotencyInFlight (another request with the same key is
//     mid-flight). This closes the concurrent-replay race the
//     plain Get/Set contract does not address.
//  3. Runs the handler.
//  4. Calls ReleaseLock(ctx, key) on exit (deferred — even on
//     handler error).
//
// The lock has a TTL so a crashing handler does not pin the key
// forever — failed locks roll off naturally. Lock TTL is tuned via
// [WithIdempotencyLockTTL] (default 30s).
//
// Stores that do NOT implement IdempotencyLocker keep the
// pre-locking behaviour (two concurrent requests may both run the
// handler — middleware assumes downstream idempotency).
//
// Opt out at the middleware via [WithIdempotencyWithoutLock] even
// when the store implements the locker.
type IdempotencyLocker interface {
	AcquireLock(ctx context.Context, key string, ttl time.Duration) (bool, error)
	ReleaseLock(ctx context.Context, key string) error
}

// IdempotencyOption tunes [IdempotencyKey].
type IdempotencyOption func(*idempotencyConfig)

type idempotencyConfig struct {
	headerName   string
	ttl          time.Duration
	methods      map[string]bool
	maxBodyBytes int
	required     bool
	skipStatuses map[int]bool
	skipLock     bool
	lockTTL      time.Duration
}

// WithIdempotencyHeader overrides the header name. Default is
// "X-Idempotency-Key" (the convention Stripe / GitHub use). Lowercase
// variants are not auto-handled — Fiber's Get is case-insensitive so
// callers don't need to worry about it.
func WithIdempotencyHeader(name string) IdempotencyOption {
	return func(c *idempotencyConfig) { c.headerName = name }
}

// WithIdempotencyTTL sets how long a captured response is replayable
// for. Default 24h. Tune down for high-volume endpoints (memory),
// up for slow-converging downstream systems (payment confirmations).
func WithIdempotencyTTL(d time.Duration) IdempotencyOption {
	return func(c *idempotencyConfig) { c.ttl = d }
}

// WithIdempotencyMethods restricts the methods the middleware caches.
// Default: POST, PUT, PATCH, DELETE — the methods that mutate state.
// Pass to add GET (read-through cache flavour) or to narrow to just
// POST.
func WithIdempotencyMethods(methods ...string) IdempotencyOption {
	return func(c *idempotencyConfig) {
		c.methods = make(map[string]bool, len(methods))
		for _, m := range methods {
			c.methods[m] = true
		}
	}
}

// WithIdempotencyMaxBodySize caps the response body the middleware
// will cache. Larger responses pass through uncached and a Warn-level
// log records the skipped key. Default 1 MiB.
//
// The cap exists because the store typically lives in Redis or
// another shared cache and an unbounded cap is a memory-pressure
// foot-gun.
func WithIdempotencyMaxBodySize(n int) IdempotencyOption {
	return func(c *idempotencyConfig) { c.maxBodyBytes = n }
}

// WithIdempotencyRequired switches the middleware into "header
// required" mode: requests without the header return 400 with Code
// [CodeIdempotencyKeyMissing] instead of passing through unaltered.
//
// Use on critical write endpoints (payment capture, refund,
// transfer) where the kit-level guarantee is part of the contract.
// Leave off on routes where the client may legitimately not care
// about idempotency.
func WithIdempotencyRequired() IdempotencyOption {
	return func(c *idempotencyConfig) { c.required = true }
}

// WithIdempotencyLockTTL sets the TTL on the SETNX-style concurrency
// lock the middleware places around in-flight handlers (when the
// store implements [IdempotencyLocker]). The lock auto-expires so
// a crashing handler does not pin the key indefinitely.
//
// Default 30s. Tune up for slow downstream calls (payment captures,
// SMS sends), down for very fast handlers. Must be shorter than the
// idempotency TTL — the lock guards the IN-FLIGHT window, not the
// replay window.
func WithIdempotencyLockTTL(d time.Duration) IdempotencyOption {
	return func(c *idempotencyConfig) { c.lockTTL = d }
}

// WithIdempotencyWithoutLock disables the IdempotencyLocker path even
// when the store implements it. Pre-lock behaviour: two concurrent
// requests with the same key may BOTH run the handler — assumes
// downstream idempotency at the DB / queue layer.
//
// Use sparingly. The locker default is the safer choice; opt out
// only on routes where the in-flight-409 response is unacceptable
// (e.g. very-long-running handlers where 409 noise drowns out
// real conflicts).
func WithIdempotencyWithoutLock() IdempotencyOption {
	return func(c *idempotencyConfig) { c.skipLock = true }
}

// WithIdempotencySkipStatus marks status codes that should NOT be
// cached. The middleware passes them through uncached so e.g. a 500
// from a transient downstream doesn't get pinned for hours.
//
// Default: 5xx are skipped (the response is not durable enough to
// replay). Calling this REPLACES the default set; pass 500, 502,
// 503, 504 etc. explicitly if you want both default + extras.
func WithIdempotencySkipStatus(statuses ...int) IdempotencyOption {
	return func(c *idempotencyConfig) {
		c.skipStatuses = make(map[int]bool, len(statuses))
		for _, s := range statuses {
			c.skipStatuses[s] = true
		}
	}
}

const (
	defaultIdempotencyHeader  = "X-Idempotency-Key"
	defaultIdempotencyTTL     = 24 * time.Hour
	defaultIdempotencyMaxBody = 1 << 20 // 1 MiB
	defaultIdempotencyLockTTL = 30 * time.Second
	replayHeader              = "X-Idempotent-Replay"
)

// IdempotencyKey returns a Fiber middleware that captures the first
// response keyed by an inbound idempotency-key header and replays the
// captured shape on every subsequent hit with the same key (within
// the TTL).
//
// Replays carry an `X-Idempotent-Replay: true` response header so
// clients (and operators inspecting traces) can distinguish them
// from a fresh handler run.
//
//	app.Post("/payments", fibermap.IdempotencyKey(store,
//	    fibermap.WithIdempotencyTTL(48*time.Hour),
//	    fibermap.WithIdempotencyRequired(),
//	), createPayment)
//
// Default-on behaviour:
//   - Header is `X-Idempotency-Key`.
//   - Replays apply to POST / PUT / PATCH / DELETE only.
//   - TTL is 24h.
//   - Response bodies > 1 MiB pass through uncached.
//   - 5xx responses are NOT cached (transient — let the next attempt
//     re-evaluate).
//   - Missing header → pass through (override with
//     [WithIdempotencyRequired]).
//
// Concurrency note: two simultaneous requests with the same key may
// BOTH run the handler — the middleware does not lock around the
// underlying store. Downstream systems must be idempotent themselves
// (the kit's transactional outbox plus DB unique constraints is the
// canonical pattern). The middleware suppresses DUPLICATE work
// across NON-overlapping requests, not concurrent ones.
func IdempotencyKey(store IdempotencyStore, opts ...IdempotencyOption) fiber.Handler {
	cfg := &idempotencyConfig{
		headerName:   defaultIdempotencyHeader,
		ttl:          defaultIdempotencyTTL,
		maxBodyBytes: defaultIdempotencyMaxBody,
		lockTTL:      defaultIdempotencyLockTTL,
		methods: map[string]bool{
			fiber.MethodPost: true, fiber.MethodPut: true,
			fiber.MethodPatch: true, fiber.MethodDelete: true,
		},
		skipStatuses: map[int]bool{
			fiber.StatusInternalServerError: true,
			fiber.StatusBadGateway:          true,
			fiber.StatusServiceUnavailable:  true,
			fiber.StatusGatewayTimeout:      true,
		},
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return func(c *fiber.Ctx) error {
		// Nil store → pass through. Lets callers wire
		// `IdempotencyKey(cache.NewIdempotencyStore(svc.Redis, ...))`
		// even on a dev box without Redis (NewIdempotencyStore
		// returns nil for nil Redis) — the route still works, just
		// without idempotency guarantees.
		if store == nil {
			return c.Next()
		}
		if !cfg.methods[c.Method()] {
			return c.Next()
		}
		key := c.Get(cfg.headerName)
		if key == "" {
			if cfg.required {
				return errs.Validation(CodeIdempotencyKeyMissing,
					"missing "+cfg.headerName+" header")
			}
			return c.Next()
		}
		if resp, _ := store.Get(c.UserContext(), key); resp != nil {
			if resp.ContentType != "" {
				c.Set(fiber.HeaderContentType, resp.ContentType)
			}
			c.Set(replayHeader, "true")
			return c.Status(resp.Status).Send(resp.Body)
		}
		// Optional concurrency lock — closes the race the plain
		// Get/Set contract doesn't cover. Two requests arriving with
		// the same key, BOTH missing the cache, would both run the
		// handler without this guard. With the locker, the second one
		// returns 409 (Code: CodeIdempotencyInFlight).
		locker, hasLocker := store.(IdempotencyLocker)
		if hasLocker && !cfg.skipLock {
			acquired, err := locker.AcquireLock(c.UserContext(), key, cfg.lockTTL)
			if err != nil {
				// Lock-acquisition failure on the backend is best-
				// effort under the same contract as Get/Set: fail
				// open so a Redis blip doesn't break writes.
				hasLocker = false
			} else if !acquired {
				return errs.Conflict(CodeIdempotencyInFlight,
					"idempotency key in flight, retry after backoff")
			}
			if hasLocker {
				defer func() { _ = locker.ReleaseLock(context.Background(), key) }()
			}
		}
		if err := c.Next(); err != nil {
			return err
		}
		status := c.Response().StatusCode()
		if cfg.skipStatuses[status] {
			return nil
		}
		body := c.Response().Body()
		if len(body) > cfg.maxBodyBytes {
			return nil
		}
		captured := make([]byte, len(body))
		copy(captured, body)
		_ = store.Set(c.UserContext(), key, &StoredResponse{
			Status:      status,
			Body:        captured,
			ContentType: string(c.Response().Header.ContentType()),
		}, cfg.ttl)
		return nil
	}
}
