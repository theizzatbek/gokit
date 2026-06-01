package fibermount

import (
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/clients/ratelimit"
	"github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
)

// Stable error Code constants returned by the rate_limit_redis factory.
const (
	// CodeRateLimitRedisInvalidArg — factory received an
	// unrecognised key-strategy token or extraneous args, OR the
	// YAML asked for "user"-keying without a subject-key fn wired
	// in via [WithRateLimitSubjectKeyFn].
	CodeRateLimitRedisInvalidArg = "fibermount_rate_limit_redis_invalid_arg"

	// CodeRateLimitRedisDenied — request hit the configured Redis
	// limit. Mapped to 429 with Retry-After by errs.HTTP.
	CodeRateLimitRedisDenied = "rate_limit_redis_denied"
)

// RateLimitRedisOption tunes [MountRateLimitRedisFactory].
type RateLimitRedisOption func(*rateLimitRedisConfig)

type rateLimitRedisConfig struct {
	subjectKeyFn auth.KeyFunc
}

// WithRateLimitSubjectKeyFn supplies the key-by-subject function so
// the YAML strategy `user` (alias `subject`) resolves correctly. The
// expected value is `auth.KeyBySubject[C]` where C is the service's
// claims type — the type-erased `auth.KeyBySubject[any]` does NOT
// work because the Locals slot holds *Principal[C], not *Principal[any].
//
// Without this option, the `user`-keying strategy returns
// CodeRateLimitRedisInvalidArg at mount time. The IP-keying strategy
// is always available.
//
//	fibermount.MountRateLimitRedisFactory(eng, limiter,
//	    fibermount.WithRateLimitSubjectKeyFn(auth.KeyBySubject[MyClaims]))
func WithRateLimitSubjectKeyFn(fn auth.KeyFunc) RateLimitRedisOption {
	return func(c *rateLimitRedisConfig) { c.subjectKeyFn = fn }
}

// MountRateLimitRedisFactory registers the `rate_limit_redis`
// middleware against eng, bound to limiter (typically a
// *ratelimit.Redis built from svc.Redis).
//
// YAML usage:
//
//	middleware:
//	  - rate_limit_redis: []                # IP-keyed (default)
//	  - rate_limit_redis: ["user"]          # subject-keyed (requires WithRateLimitSubjectKeyFn)
//	  - rate_limit_redis: ["ip", "checkout"] # IP-keyed, bucket prefix "checkout"
//
// The limit + window come from the limiter itself (set at
// ratelimit.NewRedis time), not the YAML — keeps the YAML side a
// declaration ("apply rate limiting to this route") instead of a
// duplicated config knob. To run two different budgets, build two
// limiters and register two named factories (the kit factory name is
// fixed at `rate_limit_redis`; for a second variant, call
// fibermap.RegisterMiddlewareFactory directly with a chosen name).
//
// Failure mode is OPEN: Redis transport errors log + allow the
// request through (with a backend_errors_total metric tick). A 429
// storm caused by a Redis blip would be worse than the temporary
// lapse in enforcement.
func MountRateLimitRedisFactory[T any](eng *fibermap.Engine[T], limiter ratelimit.Limiter, opts ...RateLimitRedisOption) error {
	cfg := rateLimitRedisConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	fibermap.RegisterMiddlewareFactory(eng, "rate_limit_redis",
		rateLimitRedisFactory[T](limiter, cfg))
	return nil
}

func rateLimitRedisFactory[T any](limiter ratelimit.Limiter, cfg rateLimitRedisConfig) fibermap.MiddlewareFactoryFunc[T] {
	return func(args []string) (fibermap.MiddlewareFunc[T], error) {
		strategy := "ip"
		bucket := ""
		if len(args) > 0 && args[0] != "" {
			strategy = args[0]
		}
		if len(args) > 1 {
			bucket = args[1]
		}
		if len(args) > 2 {
			return nil, errs.Validationf(CodeRateLimitRedisInvalidArg,
				"rate_limit_redis: too many args (max 2: strategy, bucket)")
		}
		keyFn, err := selectKeyFn(strategy, cfg)
		if err != nil {
			return nil, err
		}
		return func(c *fibermap.Context[T]) error {
			if limiter == nil {
				return c.Ctx.Next()
			}
			key := keyFn(c.Ctx)
			if bucket != "" {
				key = bucket + ":" + key
			}
			allow, _ := limiter.Allow(c.Ctx.UserContext(), key)
			c.Ctx.Set("X-RateLimit-Limit", strconv.Itoa(allow.Limit))
			c.Ctx.Set("X-RateLimit-Remaining", strconv.Itoa(allow.Remaining))
			if !allow.Allowed {
				retry := int(allow.RetryAfter.Seconds())
				if retry < 1 {
					retry = 1
				}
				c.Ctx.Set(fiber.HeaderRetryAfter, strconv.Itoa(retry))
				return errs.RateLimited(CodeRateLimitRedisDenied,
					"too many requests")
			}
			return c.Ctx.Next()
		}, nil
	}
}

func selectKeyFn(strategy string, cfg rateLimitRedisConfig) (auth.KeyFunc, error) {
	switch strategy {
	case "ip", "":
		return auth.KeyByIP, nil
	case "user", "subject":
		if cfg.subjectKeyFn == nil {
			return nil, errs.Validationf(CodeRateLimitRedisInvalidArg,
				"rate_limit_redis: strategy %q requires WithRateLimitSubjectKeyFn(auth.KeyBySubject[C])", strategy)
		}
		return cfg.subjectKeyFn, nil
	default:
		return nil, errs.Validationf(CodeRateLimitRedisInvalidArg,
			"rate_limit_redis: unknown key strategy %q (want ip|user)", strategy)
	}
}
