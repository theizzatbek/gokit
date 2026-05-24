package auth

import (
	"context"
	"log/slog"
	"time"

	xerrs "github.com/theizzatbek/fibermap/errs"
)

// Auth[C] is the wired auth bundle for a service. It owns the token engine,
// the refresh store reference, the password hasher reference, and all wiring
// surfaces (middleware factories, handlers).
//
// One Auth[C] per claim type per service. Two distinct *Auth values can coexist
// if a service needs separate auth domains.
type Auth[C any] struct {
	eng            *engine[C]
	store          RefreshStore
	logger         *slog.Logger
	securityLogger *slog.Logger
	cookieDomain   string
	cookiePath     string
	cookieSecure   bool
	accessTTL      time.Duration
	refreshTTL     time.Duration
	now            func() time.Time

	verifier  CredentialsVerifier[C]
	refresher ClaimsRefresher[C]
}

// CredentialsVerifier is registered by SetCredentialsVerifier. It owns user
// lookup + password check — the kit never touches the users table.
type CredentialsVerifier[C any] func(ctx context.Context, req LoginRequest) (LoginResult[C], error)

// ClaimsRefresher re-reads up-to-date scopes/roles/custom from the source of
// truth on every /refresh. Optional. When unset, refreshed access tokens carry
// only the rotated record's Subject and empty Scopes/Roles/Custom.
type ClaimsRefresher[C any] func(ctx context.Context, subject string) (LoginResult[C], error)

// LoginRequest is the body shape for /auth/login.
type LoginRequest struct {
	Login    string `json:"login"    validate:"required"`
	Password string `json:"password" validate:"required,min=1"`
}

// LoginResult is what CredentialsVerifier / ClaimsRefresher return.
type LoginResult[C any] struct {
	Subject string
	Scopes  []string
	Roles   []string
	Custom  C
}

// New constructs an Auth[C]. Returns *errs.Error{KindValidation} on misuse.
func New[C any](cfg Config, opts ...Option) (*Auth[C], error) {
	if cfg.Keys == nil {
		return nil, xerrs.Validation("missing_keys", "auth.Config.Keys is required")
	}
	if cfg.AccessTTL <= 0 {
		return nil, xerrs.Validation("invalid_ttl", "auth.Config.AccessTTL must be > 0")
	}
	if cfg.RefreshTTL <= 0 {
		return nil, xerrs.Validation("invalid_ttl", "auth.Config.RefreshTTL must be > 0")
	}
	o := options{cookiePath: "/auth"}
	for _, fn := range opts {
		fn(&o)
	}
	leeway := cfg.Leeway
	if o.leewayOverride > 0 {
		leeway = o.leewayOverride
	}
	if leeway <= 0 {
		leeway = time.Minute
	}
	now := o.now
	if now == nil {
		now = time.Now
	}
	secure := true
	if o.cookieSecure != nil {
		secure = *o.cookieSecure
	}
	a := &Auth[C]{
		eng: newEngine[C](engineConfig{
			Keys: cfg.Keys, Issuer: cfg.Issuer, Audience: cfg.Audience,
			Leeway: leeway, Now: now,
		}),
		store:          o.refreshStore,
		logger:         o.logger,
		securityLogger: o.securityLogger,
		cookieDomain:   o.cookieDomain,
		cookiePath:     o.cookiePath,
		cookieSecure:   secure,
		accessTTL:      cfg.AccessTTL,
		refreshTTL:     cfg.RefreshTTL,
		now:            now,
	}
	return a, nil
}

// Sign serialises a fully-populated Claims[C] into a JWT.
func (a *Auth[C]) Sign(c Claims[C]) (string, error) { return a.eng.sign(c) }

// Verify parses and validates a JWT and returns the typed claims.
func (a *Auth[C]) Verify(tok string) (Claims[C], error) { return a.eng.verify(tok) }

// SetCredentialsVerifier registers the project's password-check callback.
// Calling LoginHandler without one returns a 500.
func (a *Auth[C]) SetCredentialsVerifier(v CredentialsVerifier[C]) { a.verifier = v }

// SetClaimsRefresher registers an optional callback for refresh-time claim updates.
func (a *Auth[C]) SetClaimsRefresher(r ClaimsRefresher[C]) { a.refresher = r }
