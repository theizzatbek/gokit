package auth

import (
	"context"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Auth[C] is the wired auth bundle for a service. It owns the token engine,
// the refresh store reference, the password hasher reference, and all wiring
// surfaces (middleware factories, token issuance primitives).
//
// One Auth[C] per claim type per service. Two distinct *Auth values can coexist
// if a service needs separate auth domains.
type Auth[C any] struct {
	eng            *engine[C]
	store          RefreshStore
	logger         *slog.Logger
	securityLogger *slog.Logger
	metrics        *authMetrics
	cookieDomain   string
	cookiePath     string
	cookieSecure   bool
	accessTTL      time.Duration
	refreshTTL     time.Duration
	now            func() time.Time

	refresher ClaimsRefresher[C]
}

// ClaimsRefresher re-reads up-to-date scopes/roles/custom from the source of
// truth on every refresh rotation. Optional. When unset, refreshed access
// tokens carry only the rotated record's Subject and empty Scopes/Roles/Custom.
type ClaimsRefresher[C any] func(ctx context.Context, subject string) (LoginResult[C], error)

// LoginResult is what your custom login handler hands to IssueTokens /
// IssueLogin once credentials are verified. The kit never sees the wire body —
// it could be {login, password}, a PKCS7 signature, an OIDC id_token, an mTLS
// peer cert chain, whatever. Only the verified outcome matters.
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
	var m *authMetrics
	if o.metrics != nil {
		m = newAuthMetrics(o.metrics)
	}
	a := &Auth[C]{
		eng: newEngine[C](engineConfig{
			Keys: cfg.Keys, Issuer: cfg.Issuer, Audience: cfg.Audience,
			Leeway: leeway, Now: now,
		}),
		store:          o.refreshStore,
		logger:         o.logger,
		securityLogger: o.securityLogger,
		metrics:        m,
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

// SetClaimsRefresher registers an optional callback used by RotateRefresh /
// IssueRefresh to pull fresh scopes/roles/custom claims for the rotated
// subject. Without it, refreshed access tokens carry only the rotated
// record's Subject and empty Scopes/Roles/Custom.
func (a *Auth[C]) SetClaimsRefresher(r ClaimsRefresher[C]) { a.refresher = r }

// From is a method shortcut so callers can write a.From(c) without an explicit
// type parameter on the free function.
func (a *Auth[C]) From(c *fiber.Ctx) (*Principal[C], bool) { return From[C](c) }

// MustFrom is the method-shortcut for MustFrom[C](c): returns a typed
// *errs.Error{KindInternal} when the principal is absent (programmer error).
// Despite the "Must" prefix, it does NOT panic — returns an error instead.
func (a *Auth[C]) MustFrom(c *fiber.Ctx) (*Principal[C], error) { return MustFrom[C](c) }

// Subject is the method-shortcut convenience.
func (a *Auth[C]) Subject(c *fiber.Ctx) string { return Subject[C](c) }

// HasScope is the method-shortcut convenience.
func (a *Auth[C]) HasScope(c *fiber.Ctx, s string) bool { return HasScope[C](c, s) }
