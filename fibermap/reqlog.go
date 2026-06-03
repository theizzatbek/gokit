package fibermap

import (
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
)

// reqLoggerConfig is the internal config shape consumed by
// RequestLogger and RequestLoggerWithOptions.
type reqLoggerConfig struct {
	skipPaths     []string
	slowThreshold time.Duration
}

// RequestLoggerOption configures [RequestLoggerWithOptions]. The
// back-compat [RequestLogger] constructor wraps these internally.
type RequestLoggerOption func(*reqLoggerConfig)

// WithReqLogSkipPaths sets the skip-path allowlist. Equivalent to
// passing skipPaths to RequestLogger directly.
func WithReqLogSkipPaths(paths ...string) RequestLoggerOption {
	return func(c *reqLoggerConfig) { c.skipPaths = paths }
}

// WithReqLogSlowThreshold demotes fast requests (latency < d) to
// Debug level and promotes slow ones (>= d) to Warn level. 5xx
// always stays at Error regardless of latency. Default 0 = no
// threshold (every non-5xx request logged at Info — current
// behaviour).
//
// Use to keep noisy per-second polling logs out of production
// dashboards while still surfacing genuinely slow paths.
func WithReqLogSlowThreshold(d time.Duration) RequestLoggerOption {
	return func(c *reqLoggerConfig) { c.slowThreshold = d }
}

// RequestLogger returns a Fiber middleware that logs every request
// with structured fields. Backward-compatible signature; see
// [RequestLoggerWithOptions] for the option-driven form (slow-request
// threshold, etc.).
//
// Fields:
//   - method     — HTTP method
//   - path       — request path (no query string)
//   - status     — response status code
//   - latency_ms — handler latency in milliseconds
//   - bytes      — response body size
//   - request_id — value at c.Locals(LocalsRequestID), if any
//   - ip         — client IP
//
// Level: INFO for `status < 500`, ERROR otherwise. Pass nil for
// slog.Default(). `skipPaths` opts certain paths out — typically
// `/healthz` and `/metrics`.
func RequestLogger(logger *slog.Logger, skipPaths ...string) fiber.Handler {
	return RequestLoggerWithOptions(logger, WithReqLogSkipPaths(skipPaths...))
}

// RequestLoggerWithOptions is the option-driven variant of
// [RequestLogger]. Pass [WithReqLogSlowThreshold] to enable the
// slow / fast level split:
//
//   - latency < threshold → Debug
//   - latency >= threshold → Warn
//   - status >= 500 → Error (always)
func RequestLoggerWithOptions(logger *slog.Logger, opts ...RequestLoggerOption) fiber.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	cfg := reqLoggerConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	skip := make(map[string]struct{}, len(cfg.skipPaths))
	for _, p := range cfg.skipPaths {
		skip[p] = struct{}{}
	}

	return func(c *fiber.Ctx) error {
		if _, found := skip[c.Path()]; found {
			return c.Next()
		}

		start := time.Now()
		err := c.Next()
		status := c.Response().StatusCode()
		latency := time.Since(start)
		rid, _ := c.Locals(LocalsRequestID).(string)

		attrs := []slog.Attr{
			slog.String("method", c.Method()),
			slog.String("path", c.Path()),
			slog.Int("status", status),
			slog.Int64("latency_ms", latency.Milliseconds()),
			slog.Int("bytes", len(c.Response().Body())),
			slog.String("request_id", rid),
			slog.String("ip", c.IP()),
		}

		lvl := slog.LevelInfo
		switch {
		case status >= 500:
			lvl = slog.LevelError
		case cfg.slowThreshold > 0 && latency >= cfg.slowThreshold:
			lvl = slog.LevelWarn
		case cfg.slowThreshold > 0 && latency < cfg.slowThreshold:
			lvl = slog.LevelDebug
		}
		logger.LogAttrs(c.UserContext(), lvl, "http request", attrs...)
		return err
	}
}
