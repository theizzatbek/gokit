package redisclient

import (
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"github.com/theizzatbek/gokit/breaker"
)

// Option configures Connect beyond what Config covers.
type Option func(*options)

type options struct {
	logger          *slog.Logger
	metrics         prometheus.Registerer
	redisOptions    func(*redis.Options)        // single-mode mutator
	clusterMutator  func(*redis.ClusterOptions) // cluster-mode mutator
	sentinelMutator func(*redis.FailoverOptions) // sentinel-mode mutator

	extraHooks     []redis.Hook
	defaultTimeout time.Duration
	breaker        *breaker.Breaker
}

// WithLogger wires a slog.Logger. Used for connect-retry warnings and
// (when WithMetrics is also set) per-command observability via a
// Hook. nil = silent.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) { o.logger = l }
}

// WithMetrics registers Prometheus collectors on reg:
//
//   - redis_commands_total{cmd, outcome="success|error"}     (counter)
//   - redis_command_duration_seconds{cmd}                     (histogram)
//   - redis_pool_size_total{state="hits|misses|idle|stale|total"} (gauge, refreshed on scrape)
//
// Without this option, no collectors are created (zero Prometheus
// footprint). When wired, every command issued through Client.Redis()
// flows through a go-redis Hook that records the outcome — including
// commands the kit didn't issue itself (e.g. cache.Set, user code).
func WithMetrics(reg prometheus.Registerer) Option {
	return func(o *options) { o.metrics = reg }
}

// WithRedisOptions exposes the parsed *redis.Options for caller-side
// customisation before the client is constructed. Use for fields not
// expressible in the URL (PoolSize, MinIdleConns, ReadTimeout,
// custom TLSConfig, …).
//
//	redisclient.WithRedisOptions(func(o *redis.Options) {
//	    o.PoolSize = 50
//	    o.MinIdleConns = 5
//	})
func WithRedisOptions(fn func(*redis.Options)) Option {
	return func(o *options) { o.redisOptions = fn }
}

// WithClusterOptions mutates the *redis.ClusterOptions used by
// [ConnectCluster] before the cluster client is constructed. Use for
// cluster-specific tuning (RouteByLatency, RouteRandomly,
// MaxRedirects, PoolSize, ...).
//
// Single-mode Connect ignores this option; use [WithRedisOptions]
// there.
func WithClusterOptions(fn func(*redis.ClusterOptions)) Option {
	return func(o *options) { o.clusterMutator = fn }
}

// WithSentinelOptions mutates the *redis.FailoverOptions used by
// [ConnectSentinel] before the failover client is constructed.
func WithSentinelOptions(fn func(*redis.FailoverOptions)) Option {
	return func(o *options) { o.sentinelMutator = fn }
}

// WithHook appends an additional [redis.Hook] to the chain installed
// at Connect. The kit observability hook (metrics / logger /
// default-timeout / breaker) is installed first; user hooks run
// AFTER it in registration order, so they see the same (ctx, cmd)
// the kit observed. Multiple WithHook calls accumulate.
//
// Use for OTel tracing (otelredis), custom audit, or per-command
// transformation. Hooks are NOT consulted by Ping inside Connect's
// retry loop — they apply to commands issued after Connect returns.
func WithHook(h redis.Hook) Option {
	return func(o *options) {
		if h != nil {
			o.extraHooks = append(o.extraHooks, h)
		}
	}
}

// WithDefaultTimeout wraps every Redis command's context with
// context.WithTimeout(d) when the caller's ctx has NO deadline. A
// caller-supplied deadline passes through unchanged so explicit
// per-call timeouts always win.
//
// 0 (default) = no kit-level timeout (go-redis ReadTimeout /
// WriteTimeout still apply at the socket layer).
//
// Use to bound runaway commands when application code forgets to
// wrap ctx — defence-in-depth alongside per-call deadlines.
func WithDefaultTimeout(d time.Duration) Option {
	return func(o *options) { o.defaultTimeout = d }
}

// WithBreaker wraps every Redis command through the supplied
// *breaker.Breaker. The breaker classifies (err) into success /
// failure using the kit default (nil → success, redis.Nil → success,
// any other err → failure). When the breaker opens, subsequent
// commands return *errs.Error{KindUnavailable, Code:
// "redis_circuit_open"} wrapping breaker.ErrOpen — errors.Is(err,
// breaker.ErrOpen) holds.
//
// Sharing one breaker across multiple *Client instances (e.g.
// read/write split with master + replica) is supported via repeated
// WithBreaker(b) — same pattern as clients/httpc.
func WithBreaker(b *breaker.Breaker) Option {
	return func(o *options) { o.breaker = b }
}
