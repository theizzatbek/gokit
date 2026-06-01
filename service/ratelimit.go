package service

import (
	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/auth/fibermount"
	"github.com/theizzatbek/gokit/clients/ratelimit"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Stable error Code constants used by buildRateLimit.
const (
	// CodeRateLimitNeedsRedis — WithRateLimit was passed but
	// Config.Redis.URL is empty.
	CodeRateLimitNeedsRedis = "service_ratelimit_needs_redis"

	// CodeRateLimitBuildFailed — ratelimit.NewRedis returned an
	// error (e.g. malformed Config).
	CodeRateLimitBuildFailed = "service_ratelimit_build_failed"
)

// buildRateLimit constructs the kit's Redis-backed limiter when
// [WithRateLimit] was passed. Cross-validates that Redis is wired
// — a Redis-backed limiter without a Redis client is a configuration
// bug, not a degraded-mode runtime.
//
// Logger + metrics are auto-applied so the limiter's collectors
// land on the shared service registry without extra wiring.
func (s *Service[T, C]) buildRateLimit() error {
	if s.opts.rateLimitCfg == nil {
		return nil
	}
	if s.Redis == nil {
		return xerrs.Validation(CodeRateLimitNeedsRedis,
			"service: WithRateLimit requires Config.Redis.URL")
	}
	defaults := []ratelimit.Option{
		ratelimit.WithLogger(s.logger),
	}
	if s.metrics != nil {
		defaults = append(defaults, ratelimit.WithMetrics(s.metrics))
	}
	all := append(defaults, s.opts.rateLimitOpts...)
	lim, err := ratelimit.NewRedis(s.Redis, *s.opts.rateLimitCfg, all...)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeRateLimitBuildFailed,
			"service: ratelimit build failed")
	}
	s.RateLimiter = lim
	return nil
}

// mountRateLimitFactory registers the `rate_limit_redis` YAML
// middleware factory on Engine. When Auth is wired, the
// subject-keying strategy resolves via auth.KeyBySubject[C].
//
// No-op when RateLimiter is nil (caller didn't pass WithRateLimit).
// Called from buildEngine-side wiring, AFTER both buildEngine and
// the auth-factory registration so the engine already exists.
func (s *Service[T, C]) mountRateLimitFactory() error {
	if s.RateLimiter == nil {
		return nil
	}
	var opts []fibermount.RateLimitRedisOption
	if s.Auth != nil {
		opts = append(opts, fibermount.WithRateLimitSubjectKeyFn(auth.KeyBySubject[C]))
	}
	return fibermount.MountRateLimitRedisFactory(s.Engine, s.RateLimiter, opts...)
}
