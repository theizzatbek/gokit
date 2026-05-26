package natsclient

import (
	"context"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Client wraps a *nats.Conn plus a JetStream context. One Client per
// connection — share it across the process.
type Client struct {
	conn *nats.Conn
	js   nats.JetStreamContext
	opts options

	// streamCacheMu guards the subject → stream-name memoization used by
	// Publisher to decide JS-vs-core publish. Populated lazily.
	streamCacheMu sync.RWMutex
	streamCache   map[string]string

	// metrics is non-nil only when WithMetrics was supplied to Connect.
	metrics *metricsCollector
}

// Connect opens a NATS connection per cfg, opens a JetStream context, and
// returns a *Client. Returns *errs.Error on validation/transport failure.
func Connect(ctx context.Context, cfg Config, opts ...Option) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.applyDefaults()

	o := options{codec: DefaultCodec()}
	for _, fn := range opts {
		fn(&o)
	}

	// Construct metrics collector early so handler wrappers below can flip
	// the connection_status gauge on reconnect/disconnect/closed events.
	var metrics *metricsCollector
	if o.metrics != nil {
		metrics = newMetricsCollector(o.metrics)
	}

	// Wrap user reconnect/disconnect/closed handlers with internal slog hooks
	// when a logger is set, so reconnect events are visible even when the user
	// didn't register a handler.
	if o.logger != nil {
		userReconnect := o.reconnectHandler
		o.reconnectHandler = func(nc *nats.Conn) {
			o.logger.Info("nats reconnected", "url", nc.ConnectedUrl())
			if userReconnect != nil {
				userReconnect(nc)
			}
		}
		userDisconnect := o.disconnectErrHandler
		o.disconnectErrHandler = func(nc *nats.Conn, err error) {
			o.logger.Warn("nats disconnected", "err", err)
			if userDisconnect != nil {
				userDisconnect(nc, err)
			}
		}
		userClosed := o.closedHandler
		o.closedHandler = func(nc *nats.Conn) {
			o.logger.Warn("nats connection closed permanently")
			if userClosed != nil {
				userClosed(nc)
			}
		}
	}

	// Same idea but for metrics — if a metrics collector exists, track conn
	// status. This must come AFTER the logger-wrap above so both layers chain.
	if metrics != nil {
		userReconnect := o.reconnectHandler
		o.reconnectHandler = func(nc *nats.Conn) {
			metrics.SetConnectionStatus(true)
			if userReconnect != nil {
				userReconnect(nc)
			}
		}
		userDisconnect := o.disconnectErrHandler
		o.disconnectErrHandler = func(nc *nats.Conn, err error) {
			metrics.SetConnectionStatus(false)
			if userDisconnect != nil {
				userDisconnect(nc, err)
			}
		}
		userClosed := o.closedHandler
		o.closedHandler = func(nc *nats.Conn) {
			metrics.SetConnectionStatus(false)
			if userClosed != nil {
				userClosed(nc)
			}
		}
	}

	natsOpts := []nats.Option{
		nats.Name(cfg.Name),
		nats.Timeout(cfg.Timeout),
		nats.MaxReconnects(cfg.MaxReconnects),
		nats.ReconnectWait(cfg.ReconnectWait),
	}
	if cfg.Token != "" {
		natsOpts = append(natsOpts, nats.Token(cfg.Token))
	}
	if cfg.User != "" {
		natsOpts = append(natsOpts, nats.UserInfo(cfg.User, cfg.Password))
	}
	if cfg.CredsFile != "" {
		natsOpts = append(natsOpts, nats.UserCredentials(cfg.CredsFile))
	}
	if cfg.NKeySeed != "" {
		opt, err := nats.NkeyOptionFromSeed(cfg.NKeySeed)
		if err != nil {
			return nil, xerrs.Wrap(err, xerrs.KindValidation, CodeInvalidNKey, "natsclient: NKeySeed invalid")
		}
		natsOpts = append(natsOpts, opt)
	}

	if o.reconnectHandler != nil {
		natsOpts = append(natsOpts, nats.ReconnectHandler(o.reconnectHandler))
	}
	if o.disconnectErrHandler != nil {
		natsOpts = append(natsOpts, nats.DisconnectErrHandler(o.disconnectErrHandler))
	}
	if o.closedHandler != nil {
		natsOpts = append(natsOpts, nats.ClosedHandler(o.closedHandler))
	}

	var (
		conn *nats.Conn
		err  error
	)
	for attempt := 0; attempt <= cfg.ConnectMaxRetries; attempt++ {
		if attempt > 0 {
			wait := backoffWait(attempt, cfg.ConnectBackoffBase, cfg.ConnectBackoffMax)
			if o.logger != nil {
				o.logger.Warn("natsclient: connect failed, retrying",
					"attempt", attempt,
					"max_retries", cfg.ConnectMaxRetries,
					"wait", wait,
					"err", err)
			}
			select {
			case <-ctx.Done():
				return nil, xerrs.Wrap(ctx.Err(), xerrs.KindUnavailable, CodeConnectFailed, "natsclient: connect cancelled")
			case <-time.After(wait):
			}
		}
		conn, err = nats.Connect(cfg.URL, natsOpts...)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeConnectFailed, "natsclient: connect failed")
	}

	js, err := conn.JetStream()
	if err != nil {
		conn.Close()
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeJetStreamUnavailable, "natsclient: jetstream context")
	}

	return &Client{
		conn:        conn,
		js:          js,
		opts:        o,
		streamCache: make(map[string]string),
		metrics:     metrics,
	}, nil
}

// Close drains the connection (waits for in-flight pub/sub) then closes it.
// Safe to call multiple times.
func (c *Client) Close() {
	if c == nil || c.conn == nil {
		return
	}
	_ = c.conn.Drain()
	c.conn.Close()
	c.conn = nil
}

// Conn returns the underlying *nats.Conn for advanced use. Errors via this
// path are NOT funneled through *errs.Error — caller owns mapping.
func (c *Client) Conn() *nats.Conn { return c.conn }

// JetStream returns the underlying nats.JetStreamContext. Same caveat as Conn.
func (c *Client) JetStream() nats.JetStreamContext { return c.js }

// Codec returns the codec configured at Connect (JSONCodec by default).
// Exposed for cross-package use (natsmap reflects payloads at runtime
// and needs the same codec the client uses).
func (c *Client) Codec() Codec { return c.opts.codec }

// backoffWait returns the wait duration before attempt N (1-indexed).
// Exponential: base << (N-1), capped at max. Returns 0 if base <= 0.
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
