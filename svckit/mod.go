package svckit

import (
	"context"

	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Mod is everything the core is required to know about a mod. The
// rest is described by the optional interfaces below; the core probes
// for them via type assertion.
type Mod interface {
	// Name — stable identifier ("s3", "nats", "redis"). Shows up in
	// logs, Status and error texts. Unique within one New; a second
	// instance of the same mod takes its own name via WithName.
	Name() string
}

// Setuper is the earliest phase, before the logger and the HTTP
// client are built. otelmod and sentrymod live here: they hang
// slog wrappers (Host.SetLogger) and transport (Host.AddHTTPCOption)
// off the core, so they must run before their consumers do.
type Setuper interface {
	Setup(ctx context.Context, h Host) error
}

// Builder is the main phase: network clients and runtimes built on
// top of DB. By the time this runs, the logger, metrics, DB and Auth
// already exist.
type Builder interface {
	Build(ctx context.Context, h Host) error
}

// Wirer runs after the Engine is built: registering YAML middleware
// factories and mounting routes.
type Wirer interface {
	Wire(h Host) error
}

// Statuser is an optional detail for Status(). The returned value
// lands in ModStatus.Detail as-is.
type Statuser interface {
	Status() any
}

// FactoryFunc is the non-generic shape of a YAML middleware factory.
// The core wraps it into fibermap.MiddlewareFunc[T], plugging in
// c.Ctx — see registerModFactories in engine.go. The mod receives a
// bare *fiber.Ctx and knows nothing about the type parameter T.
type FactoryFunc func(args []string) (func(*fiber.Ctx) error, error)

// validateMods checks the set before anything is built: an empty
// name and duplicate names are configuration errors that must be
// caught before the first network connection.
func validateMods(mods []Mod) error {
	seen := make(map[string]struct{}, len(mods))
	for i, m := range mods {
		name := m.Name()
		if name == "" {
			return xerrs.Validationf(CodeModDuplicate,
				"svckit: mod #%d returned an empty Name()", i)
		}
		if _, dup := seen[name]; dup {
			return xerrs.Validationf(CodeModDuplicate,
				"svckit: two mods share the name %q; give one a distinct name (e.g. s3mod.WithName(%q))",
				name, name+"-backup")
		}
		seen[name] = struct{}{}
	}
	return nil
}
