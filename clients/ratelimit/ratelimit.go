package ratelimit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	redisclient "github.com/theizzatbek/gokit/clients/redis"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Allowance is the per-Allow verdict. Allowed=false is the rate-limited
// case; the caller maps it to 429 and surfaces RetryAfter via the
// Retry-After header.
//
// Remaining is the slots-left count AFTER this call succeeded (0 on
// deny). Limit + Window mirror the configured budget for header
// emission (X-RateLimit-Limit / X-RateLimit-Reset). RetryAfter is the
// suggested back-off when Allowed=false (zero on accept).
type Allowance struct {
	Allowed    bool
	Remaining  int
	Limit      int
	Window     time.Duration
	RetryAfter time.Duration
}

// Limiter is the abstract contract every rate-limiter implementation
// satisfies. Used by the fibermount factory so callers can swap a
// custom in-memory limiter in for unit tests without spinning Redis.
type Limiter interface {
	// Allow reports whether the request keyed by key may proceed.
	// Errors come from the backend (Redis transport, Lua eval
	// failures). The kit's middleware fails OPEN on error — Allowed
	// is set to true and the error is logged.
	Allow(ctx context.Context, key string) (Allowance, error)

	// Limit returns the configured per-window cap.
	Limit() int

	// Window returns the configured rolling window.
	Window() time.Duration
}

// Config configures [NewRedis]. Every field is required.
type Config struct {
	// KeyPrefix is prepended to every ZSET key. Required —
	// shared Redis serving multiple services / buckets needs
	// namespace isolation. Pass e.g. "rl:login:".
	KeyPrefix string

	// Limit is the per-window request cap. Must be > 0.
	Limit int

	// Window is the rolling window length. Must be > 0. Sub-second
	// windows work (millisecond resolution) but are rarely useful.
	Window time.Duration
}

// Redis is the default [Limiter] implementation. It runs a Lua
// sliding-window-counter script atomically per Allow. Construct with
// [NewRedis]; goroutine-safe.
type Redis struct {
	rdb    *redis.Client
	cfg    Config
	logger *slog.Logger
	script *redis.Script
	metric *metrics
}

// NewRedis returns a Redis-backed limiter. rc may be nil in dev / test
// — every Allow then succeeds (open) and logs at Debug. Returns
// *errs.Error{Code: [CodeInvalidConfig]} when cfg is malformed or
// when rc runs in cluster / sentinel mode (the Lua sliding-window
// script is single-mode-only — it pins all keys to one node).
func NewRedis(rc *redisclient.Client, cfg Config, opts ...Option) (*Redis, error) {
	if cfg.KeyPrefix == "" {
		return nil, xerrs.Validation(CodeInvalidConfig, "ratelimit: KeyPrefix is required")
	}
	if cfg.Limit <= 0 {
		return nil, xerrs.Validation(CodeInvalidConfig, "ratelimit: Limit must be > 0")
	}
	if cfg.Window <= 0 {
		return nil, xerrs.Validation(CodeInvalidConfig, "ratelimit: Window must be > 0")
	}
	r := &Redis{
		cfg:    cfg,
		script: redis.NewScript(slidingWindowScript),
	}
	if rc != nil {
		if mode := rc.Mode(); mode != redisclient.ModeSingle {
			return nil, xerrs.Validation(CodeInvalidConfig,
				"ratelimit: requires single-mode redisclient.Client; got mode="+mode.String())
		}
		r.rdb = rc.Redis()
		r.logger = rc.Logger()
	}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// Limit returns the configured cap.
func (r *Redis) Limit() int { return r.cfg.Limit }

// Window returns the configured rolling window.
func (r *Redis) Window() time.Duration { return r.cfg.Window }

// Allow reports whether key may proceed. On Redis transport / eval
// failure, returns Allowance{Allowed: true} and a wrapped error so
// the caller can decide (the kit factory ignores the error and
// records ratelimit_backend_errors_total).
//
// Nil receiver / nil rdb returns Allowance{Allowed: true} so a
// missing-Redis dev environment doesn't block all routes.
func (r *Redis) Allow(ctx context.Context, key string) (Allowance, error) {
	if r == nil || r.rdb == nil {
		return Allowance{
			Allowed:   true,
			Remaining: r.safeLimit(),
			Limit:     r.safeLimit(),
			Window:    r.safeWindow(),
		}, nil
	}
	start := time.Now()
	nonce, _ := randomNonce()
	now := time.Now().UnixMilli()
	windowMs := r.cfg.Window.Milliseconds()

	res, err := r.script.Run(ctx, r.rdb,
		[]string{r.cfg.KeyPrefix + key},
		strconv.FormatInt(now, 10),
		strconv.FormatInt(windowMs, 10),
		strconv.Itoa(r.cfg.Limit),
		nonce,
	).Result()
	r.metric.observe(time.Since(start).Seconds())
	if err != nil {
		r.metric.recordErr()
		if r.logger != nil {
			r.logger.Warn("ratelimit: redis eval failed", "key", key, "err", err.Error())
		}
		return Allowance{
				Allowed:   true,
				Remaining: r.cfg.Limit,
				Limit:     r.cfg.Limit,
				Window:    r.cfg.Window,
			}, xerrs.Wrap(err, xerrs.KindUnavailable, CodeBackendUnavailable,
				"ratelimit: backend unavailable")
	}

	arr, ok := res.([]any)
	if !ok || len(arr) < 3 {
		r.metric.recordErr()
		return Allowance{Allowed: true, Limit: r.cfg.Limit, Window: r.cfg.Window}, nil
	}
	allowed, _ := arr[0].(int64)
	remaining, _ := arr[1].(int64)
	retryMs, _ := arr[2].(int64)

	allow := Allowance{
		Allowed:    allowed == 1,
		Remaining:  int(remaining),
		Limit:      r.cfg.Limit,
		Window:     r.cfg.Window,
		RetryAfter: time.Duration(retryMs) * time.Millisecond,
	}
	if allow.Allowed {
		r.metric.record("allowed")
	} else {
		r.metric.record("denied")
	}
	return allow, nil
}

func (r *Redis) safeLimit() int {
	if r == nil {
		return 0
	}
	return r.cfg.Limit
}

func (r *Redis) safeWindow() time.Duration {
	if r == nil {
		return 0
	}
	return r.cfg.Window
}

// randomNonce produces a 16-hex-character random tag so that two
// requests arriving in the same ms each occupy their own ZSET entry.
// Falls back to a timestamped value on the (astronomically unlikely)
// rand.Read failure so we never panic in the hot path.
func randomNonce() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16), err
	}
	return hex.EncodeToString(b[:]), nil
}
