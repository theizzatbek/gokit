package fibermap

import (
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"golang.org/x/time/rate"
)

// ── CORS ──────────────────────────────────────────────────────────────

// CORSConfig configures [CORS]. Mirrors the most-used fields of
// fiber/middleware/cors with kit-friendly defaults.
type CORSConfig struct {
	// AllowOrigins is the comma-separated allowlist or "*" for any
	// origin. Default "*".
	AllowOrigins string

	// AllowMethods comma-separated. Default "GET,POST,PUT,PATCH,DELETE,OPTIONS,HEAD".
	AllowMethods string

	// AllowHeaders comma-separated. Default
	// "Origin,Content-Type,Accept,Authorization,X-Request-ID".
	AllowHeaders string

	// ExposeHeaders comma-separated. Default empty.
	ExposeHeaders string

	// AllowCredentials enables Access-Control-Allow-Credentials. When
	// true, AllowOrigins MUST NOT be "*" per the CORS spec; the kit
	// does not enforce this — operator responsibility.
	AllowCredentials bool

	// MaxAge caches the preflight response. Default 12h (43200s).
	MaxAge time.Duration
}

// CORS returns a Fiber middleware enforcing CORS headers per cfg.
// Wraps fiber/middleware/cors with kit defaults.
func CORS(cfg ...CORSConfig) fiber.Handler {
	c := CORSConfig{}
	if len(cfg) > 0 {
		c = cfg[0]
	}
	if c.AllowOrigins == "" {
		c.AllowOrigins = "*"
	}
	if c.AllowMethods == "" {
		c.AllowMethods = "GET,POST,PUT,PATCH,DELETE,OPTIONS,HEAD"
	}
	if c.AllowHeaders == "" {
		c.AllowHeaders = "Origin,Content-Type,Accept,Authorization,X-Request-ID"
	}
	if c.MaxAge == 0 {
		c.MaxAge = 12 * time.Hour
	}
	return cors.New(cors.Config{
		AllowOrigins:     c.AllowOrigins,
		AllowMethods:     c.AllowMethods,
		AllowHeaders:     c.AllowHeaders,
		ExposeHeaders:    c.ExposeHeaders,
		AllowCredentials: c.AllowCredentials,
		MaxAge:           int(c.MaxAge.Seconds()),
	})
}

// ── RateLimit ──────────────────────────────────────────────────────────

// RateLimitConfig configures [RateLimit].
type RateLimitConfig struct {
	// Max requests per Expiration window. Required (> 0).
	Max int

	// Expiration window. Default 1 minute.
	Expiration time.Duration

	// SkipPaths are exact paths to bypass rate limiting (healthz,
	// metrics, readyz). Empty = limit every path.
	SkipPaths []string

	// KeyGenerator returns the bucket key for a request. Default =
	// c.IP(). Override for per-user / per-API-key limits.
	KeyGenerator func(c *fiber.Ctx) string
}

// RateLimit returns a Fiber middleware enforcing a per-key request
// budget over a rolling window. Wraps fiber/middleware/limiter with
// kit-friendly skip-path handling — /healthz / /readyz / /metrics are
// almost always called by k8s probes and should bypass user-facing
// rate limits.
//
// Backed by go-redis/in-memory limiter; pass a Storage in
// fiber.Config or via a custom KeyGenerator+wrapper for distributed
// limits. The kit ships only the in-process baseline.
func RateLimit(cfg RateLimitConfig) fiber.Handler {
	if cfg.Expiration <= 0 {
		cfg.Expiration = time.Minute
	}
	skipSet := map[string]struct{}{}
	for _, p := range cfg.SkipPaths {
		skipSet[p] = struct{}{}
	}
	limiterCfg := limiter.Config{
		Max:        cfg.Max,
		Expiration: cfg.Expiration,
		Next: func(c *fiber.Ctx) bool {
			_, skip := skipSet[c.Path()]
			return skip
		},
	}
	if cfg.KeyGenerator != nil {
		limiterCfg.KeyGenerator = cfg.KeyGenerator
	}
	return limiter.New(limiterCfg)
}

// rateLimitByIP is a tiny in-process token-bucket alternative used by
// [WithRateLimit] when the caller passed rps + burst directly (matches
// auth.RateLimit shape). Skips configured paths.
//
// Note: this implementation is in-process; for multi-replica
// production deployments, prefer the fiber/middleware/limiter Storage
// pattern with Redis backing.
func rateLimitByIP(rps float64, burst int, skipPaths []string) fiber.Handler {
	skipSet := map[string]struct{}{}
	for _, p := range skipPaths {
		skipSet[p] = struct{}{}
	}
	limit := rate.Limit(rps)
	if burst <= 0 {
		burst = 1
	}
	limiters := &sync.Map{}
	return func(c *fiber.Ctx) error {
		if _, skip := skipSet[c.Path()]; skip {
			return c.Next()
		}
		key := c.IP()
		actual, _ := limiters.LoadOrStore(key, rate.NewLimiter(limit, burst))
		lim := actual.(*rate.Limiter)
		if !lim.Allow() {
			c.Set(fiber.HeaderRetryAfter, "1")
			return fiber.NewError(fiber.StatusTooManyRequests, "too many requests")
		}
		return c.Next()
	}
}

// ── BodyLimit ─────────────────────────────────────────────────────────

// BodyLimit returns a Fiber middleware that rejects request bodies
// larger than maxBytes with 413 Request Entity Too Large.
//
// NOTE: Fiber's recommended way to limit body size is the
// fiber.Config.BodyLimit field, which fails fast inside the parser.
// This middleware is the secondary check for cases where you set
// BodyLimit globally to a high cap but want a stricter cap on a
// specific subtree.
func BodyLimit(maxBytes int) fiber.Handler {
	if maxBytes <= 0 {
		return func(c *fiber.Ctx) error { return c.Next() }
	}
	return func(c *fiber.Ctx) error {
		if c.Request().Header.ContentLength() > maxBytes {
			return fiber.NewError(fiber.StatusRequestEntityTooLarge,
				"body exceeds maximum size")
		}
		if len(c.Body()) > maxBytes {
			return fiber.NewError(fiber.StatusRequestEntityTooLarge,
				"body exceeds maximum size")
		}
		return c.Next()
	}
}

// ── Compression ───────────────────────────────────────────────────────

// CompressionLevel mirrors the gzip/deflate level constants.
type CompressionLevel int

const (
	// CompressionDefault uses the implementation's default.
	CompressionDefault CompressionLevel = 0
	// CompressionBestSpeed minimises CPU overhead.
	CompressionBestSpeed CompressionLevel = 1
	// CompressionBestCompression maximises ratio at higher CPU cost.
	CompressionBestCompression CompressionLevel = 2
)

// Compression returns a Fiber middleware that compresses responses
// (gzip/deflate/brotli) based on the Accept-Encoding request header.
func Compression(level CompressionLevel) fiber.Handler {
	return compress.New(compress.Config{
		Level: compress.Level(level),
	})
}

// ── NotFound handler ──────────────────────────────────────────────────

// NotFoundJSON returns a route handler that emits a JSON 404 body
// matching the kit's errs.HTTP shape:
//
//	{ "code": "not_found", "message": "no route matched", "path": "..." }
//
// Suitable for both 404 (no route matched) and 405 (method not
// allowed) catch-alls. Install via [WithNotFoundHandler] in Run, or
// directly on the fiber.App via app.Use at the END of the chain.
func NotFoundJSON() fiber.Handler {
	return func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"code":    "not_found",
			"message": "no route matched",
			"path":    c.Path(),
		})
	}
}

// silenceUnused keeps strings.Builder reachable if a tightening pass
// removes the only consumer; defensive.
var _ = strings.Builder{}
