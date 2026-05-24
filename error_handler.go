package fibermap

import (
	"errors"
	"log/slog"

	"github.com/gofiber/fiber/v2"
	"github.com/theizzatbek/fibermap/errs"
)

// ErrorHandler returns a fiber.ErrorHandler that converts errors to JSON
// responses via errs.HTTP, falls back to *fiber.Error's own Code for the
// router-level errors (404, 405, ...), and auto-logs server-side failures
// (Kind >= 500 status) via the passed logger. 4xx kinds are NOT logged —
// those are normal client traffic.
//
// If logger is nil, slog.Default() is used.
func ErrorHandler(logger *slog.Logger) fiber.ErrorHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(c *fiber.Ctx, err error) error {
		status, body := resolveStatusBody(err)
		if shouldLog(status) {
			logger.Error("request failed",
				slog.Int("status", status),
				slog.String("path", c.Path()),
				slog.String("method", c.Method()),
				slog.Any("err", err),
			)
		}
		return c.Status(status).JSON(body)
	}
}

// resolveStatusBody is the composed mapper:
//   - *errs.Error (incl. wrapped) → errs.HTTP
//   - *fiber.Error → its own Code, body code "fiber_error"
//   - anything else → 500 + body code "internal_error"
func resolveStatusBody(err error) (int, errs.Response) {
	var ee *errs.Error
	if errors.As(err, &ee) {
		return errs.HTTP(err)
	}
	var fe *fiber.Error
	if errors.As(err, &fe) {
		return fe.Code, errs.Response{Code: "fiber_error", Message: fe.Message}
	}
	return errs.HTTP(err) // catch-all → (500, internal_error)
}

// shouldLog returns true when the resolved status is server-side (5xx).
// 4xx are normal client traffic and stay silent by default.
func shouldLog(status int) bool {
	return status >= 500
}
