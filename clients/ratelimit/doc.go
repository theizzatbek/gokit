// Package ratelimit is a Redis-backed sliding-window rate limiter.
//
// The Limiter interface returns Allowance{Allowed, Remaining,
// RetryAfter} per Allow call. Implementations atomic-ize the
// check-and-increment via Lua so concurrent requests across N pods
// share one shared budget.
//
// The default implementation Redis is built from a *redisclient.Client
// (kit's redis wrapper). Construct with NewRedis(rc, Config{...}, opts...);
// call Allow(ctx, key) per inbound request.
//
//	limiter, err := ratelimit.NewRedis(svc.Redis, ratelimit.Config{
//	    KeyPrefix: "rl:login:",
//	    Limit:     60,
//	    Window:    time.Minute,
//	}, ratelimit.WithLogger(svc.Logger()), ratelimit.WithMetrics(reg))
//
//	allow, _ := limiter.Allow(ctx, "user:42")
//	if !allow.Allowed {
//	    // 429 + Retry-After: allow.RetryAfter
//	}
//
// For YAML-route declaration, wire the limiter into a *fibermap.Engine
// via auth/fibermount.MountRateLimitRedisFactory and reference it as
// `rate_limit_redis` middleware in routes.yaml.
package ratelimit
