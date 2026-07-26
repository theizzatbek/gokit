// Package fibermount wires auth.Auth[C]'s middleware factories into a
// *fibermap.Engine[T]. The bridge lives in its own subpackage so the core
// auth package does not import fibermap (preserving the outward-only
// dependency direction the kit follows).
package fibermount

import (
	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/auth/authmount"
	"github.com/theizzatbek/gokit/fibermap"
)

// MountMiddlewareFactories registers bearer / require_scope / require_role
// / rate_limit against eng under those fixed names. For custom names,
// register the individual *Factory functions manually using
// fibermap.RegisterMiddlewareFactory.
//
// T is the engine's per-request data type; C is auth's custom-claims type.
// They are independent.
//
// Delegates to auth/authmount, which holds the actual wiring. This
// package keeps the same signature for backward compatibility;
// callers that don't need [MountRateLimitRedisFactory] or
// [MountIdempotencyKeyFactory] can import authmount directly instead
// and skip this package's clients/ratelimit → clients/redis
// dependency.
func MountMiddlewareFactories[T, C any](eng *fibermap.Engine[T], a *auth.Auth[C]) error {
	return authmount.MountMiddlewareFactories(eng, a)
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
//
// Delegates to auth/authmount — see [MountMiddlewareFactories] doc.
func MountAPIKeyFactory[T, C any](eng *fibermap.Engine[T], a *auth.Auth[C], store auth.KeyStore) error {
	return authmount.MountAPIKeyFactory(eng, a, store)
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
