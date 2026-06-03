// Package fibermount wires auth.Auth[C]'s middleware factories into a
// *fibermap.Engine[T]. The bridge lives in its own subpackage so the core
// auth package does not import fibermap (preserving the outward-only
// dependency direction the kit follows).
package fibermount

import (
	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/fibermap"
)

// MountMiddlewareFactories registers bearer / require_scope / require_role
// / rate_limit against eng under those fixed names. For custom names,
// register the individual *Factory functions manually using
// fibermap.RegisterMiddlewareFactory.
//
// T is the engine's per-request data type; C is auth's custom-claims type.
// They are independent.
func MountMiddlewareFactories[T, C any](eng *fibermap.Engine[T], a *auth.Auth[C]) error {
	fibermap.RegisterMiddlewareFactory(eng, "bearer", adapt[T](a.BearerFactory))
	fibermap.RegisterMiddlewareFactory(eng, "require_scope", adapt[T](a.RequireScopeFactory))
	fibermap.RegisterMiddlewareFactory(eng, "require_role", adapt[T](a.RequireRoleFactory))
	fibermap.RegisterMiddlewareFactory(eng, "require_any_scope", adapt[T](a.RequireAnyScopeFactory))
	fibermap.RegisterMiddlewareFactory(eng, "require_any_role", adapt[T](a.RequireAnyRoleFactory))
	// Use the Auth-bound factories so YAML-mounted chains feed
	// auth_ratelimit_denied_total / auth_idempotency_total when
	// auth.WithMetrics is wired. The package-level factories still
	// exist for callers that bypass fibermount.
	fibermap.RegisterMiddlewareFactory(eng, "rate_limit", adapt[T](a.RateLimitFactory))
	fibermap.RegisterMiddlewareFactory(eng, "idempotency", adapt[T](a.IdempotencyFactory))
	return nil
}

// MountAPIKeyFactory registers the `api_key` middleware factory
// against eng, bound to store. Separate from
// [MountMiddlewareFactories] because the KeyStore must be supplied
// by the caller — it's an external dependency, not a side effect of
// constructing Auth.
//
// YAML usage:
//
//	middleware:
//	  - api_key: []            # required
//	  - api_key: ["optional"]  # anonymous fallback
//
// service.WithAPIKeyStore(store) auto-calls this when both Auth and
// the supplied store are wired.
func MountAPIKeyFactory[T, C any](eng *fibermap.Engine[T], a *auth.Auth[C], store auth.KeyStore) error {
	fibermap.RegisterMiddlewareFactory(eng, "api_key", adapt[T](a.APIKeyFactory(store)))
	return nil
}

// MountIdempotencyKeyFactory registers the `idempotency_key`
// factory backed by the supplied [fibermap.IdempotencyStore] (e.g.
// `cache.NewIdempotencyStore`). The auth-side `idempotency` factory
// (already registered by [MountMiddlewareFactories]) wraps the
// in-memory store; this one wraps the cleaner fibermap.IdempotencyKey
// path with a pluggable, Redis-backed store. The two coexist —
// new code should prefer `idempotency_key`.
//
// YAML usage:
//
//	middleware:
//	  - idempotency_key: ["1h"]            # custom TTL
//	  - idempotency_key: ["1h", "required"] # require header
func MountIdempotencyKeyFactory[T any](eng *fibermap.Engine[T], store fibermap.IdempotencyStore) error {
	fibermap.RegisterMiddlewareFactory(eng, "idempotency_key",
		idempotencyKeyFactory[T](store))
	return nil
}

// adapt bridges auth's factory signature (func([]any) (fiber.Handler, error))
// to fibermap's (func([]string) (MiddlewareFunc[T], error)). YAML factory args
// always arrive as []string; we promote them to []any for the auth factory,
// then re-wrap the produced fiber.Handler so fibermap's per-request
// Context[T] is unwrapped on entry.
func adapt[T any](authFactory func([]any) (fiber.Handler, error)) fibermap.MiddlewareFactoryFunc[T] {
	return func(args []string) (fibermap.MiddlewareFunc[T], error) {
		anyArgs := make([]any, len(args))
		for i, s := range args {
			anyArgs[i] = s
		}
		h, err := authFactory(anyArgs)
		if err != nil {
			return nil, err
		}
		return func(c *fibermap.Context[T]) error { return h(c.Ctx) }, nil
	}
}
