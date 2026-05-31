package redisclient

import (
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

// Option configures Connect beyond what Config covers.
type Option func(*options)

type options struct {
	logger       *slog.Logger
	metrics      prometheus.Registerer
	redisOptions func(*redis.Options) // mutates the parsed Options before client construction
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
