package natsclient

import (
	"log/slog"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
)

// Option configures Connect beyond what Config covers.
type Option func(*options)

type options struct {
	codec   Codec
	logger  *slog.Logger
	metrics prometheus.Registerer

	// Reconnect callbacks (Task 19). Declared here to keep the type stable.
	reconnectHandler     func(*nats.Conn)
	disconnectErrHandler func(*nats.Conn, error)
	closedHandler        func(*nats.Conn)
}

// WithCodec overrides the default JSONCodec for both publish and subscribe.
// One codec per Client — keeps wire format consistent service-wide.
func WithCodec(c Codec) Option { return func(o *options) { o.codec = c } }

// WithLogger wires a slog.Logger. Used for reconnect/disconnect events,
// stream operations, handler errors (Warn), decode failures (Error), and
// consumer drift (Warn). nil = silent.
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

// WithMetrics registers Prometheus collectors on reg (Task 18 wires them up).
// Without this, no collectors are created (zero Prometheus footprint).
func WithMetrics(reg prometheus.Registerer) Option {
	return func(o *options) { o.metrics = reg }
}

// WithReconnectHandler fires after each successful reconnect.
func WithReconnectHandler(fn func(*nats.Conn)) Option {
	return func(o *options) { o.reconnectHandler = fn }
}

// WithDisconnectErrHandler fires on each disconnect, with the cause if any.
func WithDisconnectErrHandler(fn func(*nats.Conn, error)) Option {
	return func(o *options) { o.disconnectErrHandler = fn }
}

// WithClosedHandler fires when the connection is permanently closed (e.g.
// MaxReconnects exhausted).
func WithClosedHandler(fn func(*nats.Conn)) Option {
	return func(o *options) { o.closedHandler = fn }
}
