package redisclient

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Mode identifies which Redis topology the kit Client wraps. Returned
// from [Client.Mode] so callers can branch on the deployment shape
// when needed.
type Mode int

const (
	ModeSingle Mode = iota
	ModeCluster
	ModeSentinel
)

func (m Mode) String() string {
	switch m {
	case ModeSingle:
		return "single"
	case ModeCluster:
		return "cluster"
	case ModeSentinel:
		return "sentinel"
	default:
		return "unknown"
	}
}

// Client is the kit's Redis handle. Wraps a `redis.UniversalClient`
// (one of *redis.Client / *redis.ClusterClient / *redis.SentinelClient)
// + optional observability collectors. Owns the connection — call
// Close once when shutting down (idempotent + nil-safe).
type Client struct {
	universal redis.UniversalClient
	mode      Mode

	logger  *slog.Logger
	metrics *metricsCollector
}

// Connect parses cfg.URL, applies any WithRedisOptions mutator,
// constructs the underlying *redis.Client, and verifies reachability
// by issuing PING. Failures retry with exponential backoff up to
// cfg.ConnectMaxRetries, honoring ctx.Done(). Returns *errs.Error
// with one of the package-level Code constants on failure.
func Connect(ctx context.Context, cfg Config, opts ...Option) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	o := &options{}
	for _, fn := range opts {
		fn(o)
	}

	redisOpts, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, xerrs.Wrap(err, xerrs.KindValidation, CodeInvalidURL,
			"redisclient: parse URL failed")
	}
	if o.redisOptions != nil {
		o.redisOptions(redisOpts)
	}

	rdb := redis.NewClient(redisOpts)
	return finishConnect(ctx, rdb, ModeSingle, cfg.ConnectMaxRetries, cfg.ConnectBackoffBase, cfg.ConnectBackoffMax, o)
}

// ConnectCluster opens a cluster-mode connection using
// redis.NewClusterClient. Returns the same kit *Client so callers can
// move between modes by swapping Connect/ConnectCluster only; the
// rest of the surface (Universal, Close, Logger) stays identical.
//
// Observability (hook, metrics, breaker, default-timeout) works the
// same as in single-mode — go-redis routes them through every shard.
func ConnectCluster(ctx context.Context, cfg ClusterConfig, opts ...Option) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}
	clusterOpts := &redis.ClusterOptions{
		Addrs:    append([]string(nil), cfg.Addrs...),
		Username: cfg.Username,
		Password: cfg.Password,
	}
	if o.redisOptions != nil {
		// Reuse the single-node mutator by re-routing through the
		// universal shape: ClusterOptions exposes the same hot fields
		// (PoolSize, MinIdleConns, ReadTimeout, TLSConfig) so a
		// caller-supplied mutator built for *redis.Options would
		// need adaptation. We surface clusterOptions via a separate
		// option (see WithClusterOptions) so the legacy
		// WithRedisOptions stays single-mode.
	}
	if o.clusterMutator != nil {
		o.clusterMutator(clusterOpts)
	}
	rdb := redis.NewClusterClient(clusterOpts)
	return finishConnect(ctx, rdb, ModeCluster, cfg.ConnectMaxRetries, cfg.ConnectBackoffBase, cfg.ConnectBackoffMax, o)
}

// ConnectSentinel opens a Sentinel-mode failover connection using
// redis.NewFailoverClient. The resulting *Client transparently
// routes commands to the current master.
func ConnectSentinel(ctx context.Context, cfg SentinelConfig, opts ...Option) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}
	failoverOpts := &redis.FailoverOptions{
		MasterName:       cfg.MasterName,
		SentinelAddrs:    append([]string(nil), cfg.SentinelAddrs...),
		DB:               cfg.DB,
		Username:         cfg.Username,
		Password:         cfg.Password,
		SentinelUsername: cfg.SentinelUsername,
		SentinelPassword: cfg.SentinelPassword,
	}
	if o.sentinelMutator != nil {
		o.sentinelMutator(failoverOpts)
	}
	rdb := redis.NewFailoverClient(failoverOpts)
	return finishConnect(ctx, rdb, ModeSentinel, cfg.ConnectMaxRetries, cfg.ConnectBackoffBase, cfg.ConnectBackoffMax, o)
}

// finishConnect is the shared post-construction wire-up: hooks,
// metrics, ping retry. Identical across single / cluster / sentinel
// modes because every universal client implements AddHook + Ping +
// Close uniformly.
func finishConnect(ctx context.Context, rdb redis.UniversalClient, mode Mode, retries int, base, max time.Duration, o *options) (*Client, error) {
	c := &Client{
		universal: rdb,
		mode:      mode,
		logger:    o.logger,
	}
	if o.metrics != nil {
		c.metrics = newMetricsCollector(o.metrics, rdb)
	}
	// Install the kit observability hook when EITHER logger or
	// metrics is wired. metricsCollector is nil-safe inside the hook
	// so logger-only mode still works. defaultTimeout / breaker wrap
	// into the same hook chain.
	needHook := o.metrics != nil || o.logger != nil || o.defaultTimeout > 0 || o.breaker != nil
	if needHook {
		rdb.AddHook(newHook(c.metrics, o.logger, o.defaultTimeout, o.breaker))
	}
	for _, h := range o.extraHooks {
		if h != nil {
			rdb.AddHook(h)
		}
	}

	// Retry the initial ping. Same backoff semantics as nats / db.
	var pingErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			wait := backoffWait(attempt, base, max)
			if o.logger != nil {
				o.logger.Warn("redisclient: connect failed, retrying",
					"mode", mode.String(),
					"attempt", attempt,
					"max_retries", retries,
					"wait", wait,
					"err", pingErr)
			}
			select {
			case <-ctx.Done():
				_ = rdb.Close()
				return nil, xerrs.Wrap(ctx.Err(), xerrs.KindUnavailable, CodeConnectFailed,
					"redisclient: connect cancelled")
			case <-time.After(wait):
			}
		}
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		pingErr = rdb.Ping(pingCtx).Err()
		cancel()
		if pingErr == nil {
			if c.metrics != nil {
				c.metrics.setConnectionStatus(true)
			}
			return c, nil
		}
	}
	_ = rdb.Close()
	return nil, xerrs.Wrap(pingErr, xerrs.KindUnavailable, CodeConnectFailed,
		"redisclient: ping failed after retries")
}

// Logger returns the *slog.Logger configured via WithLogger, or nil
// when none was supplied. Nil-receiver safe — returns nil.
func (c *Client) Logger() *slog.Logger {
	if c == nil {
		return nil
	}
	return c.logger
}

// Redis returns the underlying *redis.Client when the kit Client
// runs in single-node mode (the default). Panics with a guiding
// message under cluster / sentinel modes — use [Client.Universal]
// there. Nil-receiver safe (returns nil).
//
// The panic is intentional: a caller asking for the single-mode
// type under cluster/sentinel is a programmer error and silently
// returning nil leaks the bug to a later nil-deref far from the
// call site. Branch on [Client.Mode] (or use [Client.Universal])
// in code that may run under multiple topologies.
func (c *Client) Redis() *redis.Client {
	if c == nil {
		return nil
	}
	single, ok := c.universal.(*redis.Client)
	if !ok {
		panic("clients/redis: Redis() is single-mode only; got mode=" + c.mode.String() +
			" — use Client.Universal() for cluster/sentinel topologies")
	}
	return single
}

// Universal returns the underlying redis.UniversalClient regardless
// of mode. Use as the cross-mode escape hatch (HSet / Pipeline /
// Subscribe etc are all on the interface). Single-mode callers can
// still use Redis() for *redis.Client-only APIs.
func (c *Client) Universal() redis.UniversalClient {
	if c == nil {
		return nil
	}
	return c.universal
}

// Mode reports which Redis topology the kit Client wraps. Use to
// branch on mode when an API is only meaningful for single-node
// (e.g. SUBSCRIBE-style PubSub through *redis.Client).
func (c *Client) Mode() Mode {
	if c == nil {
		return ModeSingle
	}
	return c.mode
}

// Close releases the underlying connection pool. Idempotent +
// nil-safe — service.Close calls this unconditionally.
func (c *Client) Close() error {
	if c == nil || c.universal == nil {
		return nil
	}
	if c.metrics != nil {
		c.metrics.setConnectionStatus(false)
	}
	err := c.universal.Close()
	c.universal = nil
	return err
}

// backoffWait returns the wait duration for attempt N (1-indexed).
// base doubles per attempt up to max. base <= 0 short-circuits to 0
// (no wait between attempts).
func backoffWait(attempt int, base, max time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	w := base << (attempt - 1)
	if w <= 0 || w > max {
		return max
	}
	return w
}
