package svckit

import (
	"log/slog"

	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/clients/httpc"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/fibermap"
)

// Host is everything the core gives a mod. The interface is
// deliberately free of type parameters: a mod must not know either T
// (the engine's payload type) or C (claims). Where a generic is still
// needed — the YAML middleware factory and subject-based keying — the
// core hands back a non-generic slice instead.
type Host interface {
	// Logger — the service logger at call time. otelmod/sentrymod
	// may replace it via SetLogger during the Setup phase, and every
	// subsequent mod receives the already-wrapped one.
	Logger() *slog.Logger
	SetLogger(*slog.Logger)

	Metrics() prometheus.Registerer

	// DB — nil when Config.DB.User is empty. A mod must check this:
	// webhooks and outbox cannot be built without a DB.
	DB() *db.DB

	// SubjectKeyFn — the non-generic slice of Auth: a rate-limit key
	// derived from the JWT subject. nil when Auth is not configured.
	SubjectKeyFn() func(*fiber.Ctx) string

	NodeName() string
	ServerGroup() string

	// ResolvePath — the same logic the core uses for routes.yaml: an
	// explicit path wins, otherwise defaultName inside ConfigsDir,
	// otherwise empty (subsystem disabled).
	ResolvePath(userPath, defaultName string, enabled bool) string

	// AddHTTPCOption affects the HTTP client the core builds after
	// the Setup phase. Calling this from Build is already too late.
	AddHTTPCOption(...httpc.Option)

	// UseFiber — app-level middleware, outside the engine.
	UseFiber(...fiber.Handler)

	// AddRunOption — anything that ends up in Engine.Run: mounting
	// routes, e.g. an HTTP→NATS gateway.
	AddRunOption(...fibermap.RunOption)

	AddReadinessChecker(...fibermap.Checker)

	// RegisterMiddlewareFactory — a YAML middleware factory under the
	// name name. The core adapts it to the engine's type.
	RegisterMiddlewareFactory(name string, f FactoryFunc)

	// OnShutdown — resource cleanup, LIFO relative to registration
	// order. nil is ignored.
	OnShutdown(func() error)
}
