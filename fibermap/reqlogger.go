package fibermap

import (
	"log/slog"

	"github.com/gofiber/fiber/v2"
)

// LocalsLogger is the key under which [RequestLogger] stores the
// request-scoped *slog.Logger. Exported so subsystems that already
// hold a *fiber.Ctx can bypass [LoggerFrom] and read the slot
// directly.
const LocalsLogger = "fibermap_request_logger"

// LocalsAuthSubject is the conventional key under which the kit's
// auth middleware stores the authenticated subject string. Read by
// [LoggerFrom] when no other identifier is present.
const LocalsAuthSubject = "fibermap_auth_subject"

// LoggerInjector returns a middleware that derives a request-scoped
// *slog.Logger from base and stores it under [LocalsLogger]. Every
// log statement made via [LoggerFrom] in downstream handlers
// automatically carries:
//
//   - method
//   - path (routed pattern when matched, raw URI otherwise)
//   - request_id (when [RequestID] middleware populated it)
//
// Other attrs (user_id, route_name) are NOT bound at install time
// because they may not exist yet — they're added lazily inside
// [LoggerFrom] from whatever Locals state is present at the call
// site.
//
// nil base falls back to slog.Default at logger construction time.
//
// Distinct from [RequestLogger]: that one EMITS access logs at
// request end; this one ATTACHES a logger handlers can use mid-
// request.
//
//	app.Use(fibermap.RequestID())
//	app.Use(fibermap.LoggerInjector(svc.Logger()))
//	// later, in a handler:
//	logger := fibermap.LoggerFrom(c)
//	logger.Info("created link", "code", code)
//	// emits {... method=POST path=/links request_id=... user_id=... code=...}
func LoggerInjector(base *slog.Logger) fiber.Handler {
	if base == nil {
		base = slog.Default()
	}
	return func(c *fiber.Ctx) error {
		rid, _ := c.Locals(LocalsRequestID).(string)
		attrs := []any{
			slog.String("method", c.Method()),
			slog.String("path", c.Path()),
		}
		if rid != "" {
			attrs = append(attrs, slog.String("request_id", rid))
		}
		l := base.With(attrs...)
		c.Locals(LocalsLogger, l)
		return c.Next()
	}
}

// LoggerFrom returns the request-scoped logger installed by
// [RequestLogger], enriched with the subject from the kit's auth
// principal at call time when available. Falls back to slog.Default
// when no per-request logger is present (e.g. the middleware wasn't
// installed, or the call site is outside the request lifecycle).
//
// Subject discovery order:
//
//  1. c.Locals(LocalsAuthSubject) — wire this from your auth
//     middleware once and every LoggerFrom call inherits it.
//  2. fall through with no user_id attr.
//
// Route name discovery: c.Route().Name when set (programmatic
// routes / YAML routes both populate this). Suppressed when empty
// to keep log lines clean.
func LoggerFrom(c *fiber.Ctx) *slog.Logger {
	if c == nil {
		return slog.Default()
	}
	base, ok := c.Locals(LocalsLogger).(*slog.Logger)
	if !ok || base == nil {
		base = slog.Default()
	}
	var attrs []any
	if subj, _ := c.Locals(LocalsAuthSubject).(string); subj != "" {
		attrs = append(attrs, slog.String("user_id", subj))
	}
	if r := c.Route(); r != nil && r.Name != "" {
		attrs = append(attrs, slog.String("route", r.Name))
	}
	if len(attrs) == 0 {
		return base
	}
	return base.With(attrs...)
}
