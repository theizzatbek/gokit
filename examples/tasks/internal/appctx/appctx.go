// Package appctx defines the per-request payload type for this example
// and project-local type aliases so handler signatures don't carry
// the generic parameter everywhere.
package appctx

import (
	"log/slog"

	"github.com/theizzatbek/gokit/fibermap"
)

// AppCtx is the payload built by ContextBuilder for every request.
// In a real app you'd typically include: authenticated user, request
// ID, a scoped logger, a request-scoped database transaction, etc.
type AppCtx struct {
	UserID    string
	Role      string
	RequestID string

	// Log is a request-scoped slog logger with user_id + request_id
	// already in its With(...). Handlers can call c.Data.Log.Info(...)
	// instead of threading context through every call.
	Log *slog.Logger
}

// Ctx, H, MW hide the generic parameter behind project-local aliases.
// Handlers/middleware register as `func(c *Ctx) error` instead of
// `func(c *fibermap.Context[AppCtx]) error`.
type (
	Ctx = fibermap.Context[AppCtx]
	H   = fibermap.HandlerFunc[AppCtx]
	MW  = fibermap.MiddlewareFunc[AppCtx]
)
