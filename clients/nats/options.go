package natsclient

import (
	"crypto/tls"
	"crypto/x509"
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

	// TLS material — combine WithTLSConfig to pass everything verbatim
	// OR WithRootCAs + WithClientCert to build it piecewise.
	tlsConfig      *tls.Config
	rootCAs        *x509.CertPool
	clientCertFile string
	clientKeyFile  string
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

// WithTLSConfig passes a fully-built *tls.Config to the underlying
// connection. Mutually exclusive with WithRootCAs / WithClientCert —
// pass everything verbatim or compose piecewise, not both.
//
// Use when you need fine-grained control (custom VerifyPeerCertificate,
// session cache, MinVersion pinning beyond defaults).
func WithTLSConfig(cfg *tls.Config) Option {
	return func(o *options) { o.tlsConfig = cfg }
}

// WithRootCAs supplies the trust pool used to verify the NATS
// server's certificate. nats-server with self-signed / private-CA
// certs require this; managed NATS (Synadia, etc.) usually does not.
func WithRootCAs(pool *x509.CertPool) Option {
	return func(o *options) { o.rootCAs = pool }
}

// WithClientCert enables mutual-TLS (mTLS) — the client presents the
// supplied cert/key pair during the TLS handshake. nats-server can
// then enforce auth via the cert subject (no Token/User/Creds needed).
//
// Both paths are required; partial wiring returns *errs.Error at
// Connect.
func WithClientCert(certFile, keyFile string) Option {
	return func(o *options) {
		o.clientCertFile = certFile
		o.clientKeyFile = keyFile
	}
}
