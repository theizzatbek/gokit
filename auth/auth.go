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
	eng              *engine[C]
	store            RefreshStore
	logger           *slog.Logger
	securityLogger   *slog.Logger
	metrics          *authMetrics
	cookieDomain     string
	cookiePath       string
	cookieSecure     bool
	accessTTL        time.Duration
	refreshTTL       time.Duration
	now              func() time.Time
	apiKeyHashSecret []byte

	refresher     ClaimsRefresher[C]
	revokedAccess RevokedAccessStore
	ipExtractor   IPExtractor
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
		store:            o.refreshStore,
		logger:           o.logger,
		securityLogger:   o.securityLogger,
		metrics:          m,
		cookieDomain:     o.cookieDomain,
		cookiePath:       o.cookiePath,
		cookieSecure:     secure,
		accessTTL:        cfg.AccessTTL,
		refreshTTL:       cfg.RefreshTTL,
		now:              now,
		apiKeyHashSecret: cfg.APIKeyHashSecret,
		revokedAccess:    o.revokedAccess,
		ipExtractor:      o.ipExtractor,
	}
	return a, nil
}

// Sign serialises a fully-populated Claims[C] into a JWT.
func (a *Auth[C]) Sign(c Claims[C]) (string, error) { return a.eng.sign(c) }

// Verify parses and validates a JWT and returns the typed claims.
func (a *Auth[C]) Verify(tok string) (Claims[C], error) { return a.eng.verify(tok) }

// KeySet returns the currently-active KeySet via an atomic load.
// Useful for callers that want to serve the kit's JWKS document
// themselves (the canonical handler is Auth.JWKSHandler).
func (a *Auth[C]) KeySet() *KeySet { return a.eng.keySet() }

// RotateKeys hot-swaps the signing/verification key set without
// dropping in-flight Sign / Verify calls. Use during operator-driven
// rotation (KMS / Vault publishes a new active key; the old kid stays
// in the verify set until grace expires; eventually it's dropped).
//
// Returns *errs.Error{KindValidation, …} when:
//
//   - ks is nil
//   - ks.active.Priv == nil (active kid is verify-only)
//   - ks has zero verify entries
//
// Otherwise the swap is atomic — the next Sign uses the new active
// key, the next Verify accepts every kid in the new verify set. Any
// Sign / Verify already running against the OLD KeySet completes
// against that pointer; the next call to Sign or Verify picks up the
// new one. No mutex, no blocking.
func (a *Auth[C]) RotateKeys(ks *KeySet) error {
	if ks == nil {
		return xerrs.Validation("invalid_keys", "auth: RotateKeys nil KeySet")
	}
	if len(ks.verify) == 0 {
		return xerrs.Validation("invalid_keys", "auth: RotateKeys KeySet has no verify entries")
	}
	if ks.active.KID != "" && ks.active.Priv == nil {
		return xerrs.Validation("invalid_keys", "auth: RotateKeys active key is verify-only (no private material)")
	}
	a.eng.rotateKeys(ks)
	return nil
}

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
