package fibermap

import (
	"log/slog"
	"runtime/debug"

	"github.com/gofiber/fiber/v2"
	fiberrecover "github.com/gofiber/fiber/v2/middleware/recover"
)

// Recover returns a Fiber middleware that traps panics in downstream
// handlers and converts them to a 500 response. It wraps Fiber's
// stock `middleware/recover` with stack traces enabled and a
// `*slog.Logger`-aware stack handler.
//
// If `logger` is nil, panic + stack go to the default slog logger
// (slog.Default). The log includes the request method, path, and
// request_id (when [RequestID] populated it).
//
// Install as early as possible — typically first in the chain so it
// catches panics in any other middleware too. The `WithRecover`
// `RunOption` does this for you.
//
//	eng.Run(fibermap.WithRecover(logger), ...)
//
// Or directly:
//
//	app.Use(fibermap.Recover(logger))
func Recover(logger *slog.Logger) fiber.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return fiberrecover.New(fiberrecover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(c *fiber.Ctx, e any) {
			rid, _ := c.Locals(LocalsRequestID).(string)
			logger.Error("panic recovered",
				slog.Any("panic", e),
				slog.String("method", c.Method()),
				slog.String("path", c.Path()),
				slog.String("request_id", rid),
				slog.String("stack", string(debug.Stack())),
			)
		},
	})
}
