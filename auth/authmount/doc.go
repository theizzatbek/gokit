// Package authmount wires auth.Auth[C]'s core middleware factories —
// bearer, require_scope, require_role, require_any_scope,
// require_any_role, rate_limit, idempotency, and api_key — into a
// *fibermap.Engine[T].
//
// It exists as the Redis-free half of auth/fibermount. That package
// also hosts ratelimit_redis.go (MountRateLimitRedisFactory), which
// imports clients/ratelimit → clients/redis; Go resolves dependencies
// per package, so importing auth/fibermount at all — even only for
// MountMiddlewareFactories — drags both files' dependencies along.
// authmount holds exactly the core wiring and imports only auth,
// fibermap, and fiber.
//
// auth/fibermount.MountMiddlewareFactories and
// auth/fibermount.MountAPIKeyFactory delegate to this package for
// backward compatibility. Callers that don't need
// MountRateLimitRedisFactory or MountIdempotencyKeyFactory should
// import authmount directly to keep clients/ratelimit and
// clients/redis out of their binary.
package authmount
