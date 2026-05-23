package fibermap

import (
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
)

// RequestLogger returns a Fiber middleware that logs every request
// with structured fields:
//
//   - method     — HTTP method
//   - path       — request path (no query string)
//   - status     — response status code
//   - latency_ms — handler latency in milliseconds
//   - bytes      — response body size
//   - request_id — value at c.Locals(LocalsRequestID), if any
//   - ip         — client IP (Fiber's c.IP, respects X-Forwarded-For
//     config on fiber.New)
//
// Level is INFO for `status < 500`, ERROR otherwise. Pass nil for
// slog.Default(). Install with [WithRequestLogger] or directly:
//
//	app.Use(fibermap.RequestLogger(logger))
//
// For per-request log enrichment (user_id, org_id, …), set
// [Engine.SetContextBuilder] to build an `AppCtx.Log = logger.With(...)`
// and use that scoped logger inside your handlers. RequestLogger is
// the "one access-log line per request" surface — it does not
// duplicate the per-request scoped logger.
//
// `skipPaths` opts certain paths out — typically `/healthz` and
// `/metrics` whose logs are uninteresting and noisy.
func RequestLogger(logger *slog.Logger, skipPaths ...string) fiber.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	skip := make(map[string]struct{}, len(skipPaths))
	for _, p := range skipPaths {
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
		if status >= 500 {
			lvl = slog.LevelError
		}
		logger.LogAttrs(c.UserContext(), lvl, "http request", attrs...)
		return err
	}
}
