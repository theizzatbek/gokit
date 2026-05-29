package auth

import (
	"strconv"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/time/rate"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// KeyFunc resolves a *fiber.Ctx to the bucket key. Used by [RateLimitBy].
// Implementations should be stable and fast (called on every request).
type KeyFunc func(c *fiber.Ctx) string

// KeyByIP keys the limiter on the client IP — Fiber resolves through
// the configured `X-Forwarded-For` chain when behind a trusted proxy.
// Use as the default for anonymous endpoints (login, register).
func KeyByIP(c *fiber.Ctx) string { return c.IP() }

// KeyBySubject keys the limiter on the authenticated principal's
// subject (the JWT `sub` claim). Anonymous requests fall back to the
// client IP — protecting the same endpoint with the same limiter for
// both authenticated and pre-auth traffic. Mount the auth.Bearer
// middleware BEFORE the rate limiter so the principal is populated.
func KeyBySubject[C any](c *fiber.Ctx) string {
	if s := Subject[C](c); s != "" {
		return "sub:" + s
	}
	return "ip:" + c.IP()
}

// RateLimit returns a Fiber middleware that enforces a token-bucket
// rate limit keyed by client IP. Convenience for [RateLimitBy]
// with [KeyByIP].
//
//	app.Post("/auth/login", auth.RateLimit(5, 10), loginHandler)
//	// → 5 req/s sustained, burst 10
//
// On exceeded limit: returns *errs.Error{KindRateLimited,
// Code: CodeRateLimited} → 429 with stable wire shape, plus a
// Retry-After header conservatively suggesting one full token's
// recovery time.
func RateLimit(rps float64, burst int) fiber.Handler {
	return RateLimitBy(rps, burst, KeyByIP)
}

// RateLimit (method form) is the *Auth[C]-bound convenience for the
// package-level RateLimit. Unlike the free function, this variant
// increments `auth_ratelimit_denied_total` on each rejection when
// [WithMetrics] was wired — prefer it over the package-level form
// in services that scrape metrics.
func (a *Auth[C]) RateLimit(rps float64, burst int) fiber.Handler {
	return rateLimitBy(rps, burst, KeyByIP, a.metrics)
}

// RateLimitBySubject keys the limiter on the authenticated principal.
// Anonymous requests fall back to client IP. Mount auth.Bearer
// upstream so the principal is populated when present.
//
// Counts denials in `auth_ratelimit_denied_total` when [WithMetrics]
// is wired.
func (a *Auth[C]) RateLimitBySubject(rps float64, burst int) fiber.Handler {
	return rateLimitBy(rps, burst, KeyBySubject[C], a.metrics)
}

// RateLimitBy is the explicit-key variant of [RateLimit]. keyFn picks
// the bucket per request; one *rate.Limiter is created per unique key
// (lazy, sync.Map). The bucket recovers at rps tokens/second up to
// burst.
//
// Memory note: limiters are NEVER evicted in this implementation —
// suitable for the typical service shape (bounded IP set, hundreds of
// thousands of subjects). For internet-facing services with
// effectively unbounded IP space, front the kit with a real rate
// limiter (envoy, redis-cell, …) or wrap RateLimitBy with your own
// LRU + cleanup goroutine.
func RateLimitBy(rps float64, burst int, keyFn KeyFunc) fiber.Handler {
	return rateLimitBy(rps, burst, keyFn, nil)
}

// rateLimitBy is the shared implementation; m is the optional
// authMetrics instance for denial counting (nil-safe via authMetrics
// receiver methods).
func rateLimitBy(rps float64, burst int, keyFn KeyFunc, m *authMetrics) fiber.Handler {
	if keyFn == nil {
		keyFn = KeyByIP
	}
	if burst <= 0 {
		burst = 1
	}
	limit := rate.Limit(rps)
	limiters := &sync.Map{}

	return func(c *fiber.Ctx) error {
		key := keyFn(c)
		actual, _ := limiters.LoadOrStore(key, rate.NewLimiter(limit, burst))
		lim := actual.(*rate.Limiter)
		if !lim.Allow() {
			m.incRateLimitDenied()
			c.Set(fiber.HeaderRetryAfter, retryAfterFor(lim))
			return xerrs.RateLimited(CodeRateLimited, "too many requests")
		}
		return c.Next()
	}
}

// retryAfterFor returns a conservative Retry-After (seconds) hint —
// how long until the next token arrives. Always a positive integer per
// RFC 7231 §7.1.3.
func retryAfterFor(lim *rate.Limiter) string {
	r := lim.Limit()
	if r <= 0 {
		return "1"
	}
	wait := time.Duration(float64(time.Second) / float64(r))
	secs := int(wait.Seconds())
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}

// RateLimitFactory adapts RateLimit to fibermap's middleware-factory
// signature. Accepts the same two arguments as [RateLimit] but in
// stringified form so they fit a YAML scalar list:
//
//	middleware:
//	  - rate_limit: ["5", "10"]    # 5 req/s, burst 10, keyed by IP
//
// First arg is rps (float64-parseable), second is burst (int). On
// parse failure returns *errs.Error{Code: CodeInvalidFactoryArgs};
// fibermap surfaces it at Mount time. To key by subject instead, build
// a custom factory that calls RateLimitBy with KeyBySubject[YourClaims].
func RateLimitFactory(args []any) (fiber.Handler, error) {
	rps, burst, err := parseRateLimitArgs(args)
	if err != nil {
		return nil, err
	}
	return RateLimit(rps, burst), nil
}

// RateLimitFactory (method form) is the *Auth[C]-bound variant of
// the package-level [RateLimitFactory]. Use this when wiring rate
// limiting through YAML and you want the resulting limiter to feed
// `auth_ratelimit_denied_total`. fibermount registers this variant
// for you when you call MountMiddlewareFactories.
func (a *Auth[C]) RateLimitFactory(args []any) (fiber.Handler, error) {
	rps, burst, err := parseRateLimitArgs(args)
	if err != nil {
		return nil, err
	}
	return a.RateLimit(rps, burst), nil
}

func parseRateLimitArgs(args []any) (float64, int, error) {
	if len(args) != 2 {
		return 0, 0, xerrs.Internalf(CodeInvalidFactoryArgs,
			"rate_limit: expected [rps, burst], got %d args", len(args))
	}
	rps, err := factoryFloat(args[0], "rate_limit", "rps")
	if err != nil {
		return 0, 0, err
	}
	burst, err := factoryInt(args[1], "rate_limit", "burst")
	if err != nil {
		return 0, 0, err
	}
	return rps, burst, nil
}

func factoryFloat(v any, name, field string) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case string:
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return 0, xerrs.Internalf(CodeInvalidFactoryArgs,
				"%s: %s = %q is not a float", name, field, x)
		}
		return f, nil
	}
	return 0, xerrs.Internalf(CodeInvalidFactoryArgs,
		"%s: %s has unsupported type %T (want number or numeric string)", name, field, v)
}

func factoryInt(v any, name, field string) (int, error) {
	switch x := v.(type) {
	case int:
		return x, nil
	case float64:
		return int(x), nil
	case string:
		n, err := strconv.Atoi(x)
		if err != nil {
			return 0, xerrs.Internalf(CodeInvalidFactoryArgs,
				"%s: %s = %q is not an int", name, field, x)
		}
		return n, nil
	}
	return 0, xerrs.Internalf(CodeInvalidFactoryArgs,
		"%s: %s has unsupported type %T (want int or numeric string)", name, field, v)
}
