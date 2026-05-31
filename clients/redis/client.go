package redisclient

import (
	"context"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Client is the kit's Redis handle. Wraps a *redis.Client + optional
// observability collectors. Owns the connection — call Close once
// when shutting down (idempotent + nil-safe).
type Client struct {
	rdb     *redis.Client
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

	c := &Client{
		rdb:    rdb,
		logger: o.logger,
	}
	if o.metrics != nil {
		c.metrics = newMetricsCollector(o.metrics, rdb)
	}
	// Install the observability hook when EITHER logger or metrics
	// is wired. metricsCollector is nil-safe inside the hook so
	// logger-only mode still works.
	if o.metrics != nil || o.logger != nil {
		rdb.AddHook(newHook(c.metrics, o.logger))
	}

	// Retry the initial ping. Same backoff semantics as nats / db.
	var pingErr error
	for attempt := 0; attempt <= cfg.ConnectMaxRetries; attempt++ {
		if attempt > 0 {
			wait := backoffWait(attempt, cfg.ConnectBackoffBase, cfg.ConnectBackoffMax)
			if o.logger != nil {
				o.logger.Warn("redisclient: connect failed, retrying",
					"attempt", attempt,
					"max_retries", cfg.ConnectMaxRetries,
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
			return c, nil
		}
	}
	_ = rdb.Close()
	return nil, xerrs.Wrap(pingErr, xerrs.KindUnavailable, CodeConnectFailed,
		"redisclient: ping failed after retries")
}

// Redis returns the underlying *redis.Client. Use any go-redis API
// the wrapper doesn't expose directly. The lifetime is owned by
// *Client; do NOT close the returned client yourself.
func (c *Client) Redis() *redis.Client {
	if c == nil {
		return nil
	}
	return c.rdb
}

// Close releases the underlying connection pool. Idempotent +
// nil-safe — service.Close calls this unconditionally.
func (c *Client) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	err := c.rdb.Close()
	c.rdb = nil
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
