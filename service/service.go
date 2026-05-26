package service

import (
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/clients/apimap"
	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/fibermap"
)

// Service is the bundled runtime. Public fields are nil for subsystems
// that weren't opted into via Config; check before use.
type Service[T any, C any] struct {
	DB      *db.DB              // nil when Config.DB.User == ""
	Auth    *auth.Auth[C]       // nil when Config.Auth.PrivateKeyPEM == ""
	NATS    *natsclient.Client  // nil when Config.NATS.URL == ""
	NATSMap *natsmap.Runtime    // nil when Config.NATSMap.*Path is empty
	HTTPC   *http.Client        // always built
	APIMap  *apimap.Client      // nil when Config.APIMap.Path == ""
	Engine  *fibermap.Engine[T] // always built
	Hasher  *auth.Hasher        // nil when Auth is nil

	cfg     Config
	logger  *slog.Logger
	metrics prometheus.Registerer
	opts    *options

	closed bool
}

// Logger returns the *slog.Logger Service constructed (or the one
// supplied via WithLogger).
func (s *Service[T, C]) Logger() *slog.Logger { return s.logger }

// Metrics returns the prometheus.Registerer Service constructed (or
// the one supplied via WithMetrics).
func (s *Service[T, C]) Metrics() prometheus.Registerer { return s.metrics }

// SetContextBuilder is the typed proxy for Engine.SetContextBuilder.
func (s *Service[T, C]) SetContextBuilder(fn fibermap.ContextBuilder[T]) {
	s.Engine.SetContextBuilder(fn)
}

// SetCredentialsVerifier is the typed proxy for Auth.SetCredentialsVerifier.
// Panics with a clear message if Auth was not configured (programmer error).
func (s *Service[T, C]) SetCredentialsVerifier(v auth.CredentialsVerifier[C]) {
	if s.Auth == nil {
		panic("service: SetCredentialsVerifier called but Config.Auth.PrivateKeyPEM is empty")
	}
	s.Auth.SetCredentialsVerifier(v)
}

// SetClaimsRefresher is the typed proxy for Auth.SetClaimsRefresher.
func (s *Service[T, C]) SetClaimsRefresher(r auth.ClaimsRefresher[C]) {
	if s.Auth == nil {
		panic("service: SetClaimsRefresher called but Config.Auth.PrivateKeyPEM is empty")
	}
	s.Auth.SetClaimsRefresher(r)
}
