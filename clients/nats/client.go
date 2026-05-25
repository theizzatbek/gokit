package natsclient

import (
	"context"
	"sync"

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

	conn, err := nats.Connect(cfg.URL, natsOpts...)
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
