package auth

import (
	"log/slog"
	"time"
)

// Option configures New beyond what Config covers.
type Option func(*options)

type options struct {
	refreshStore    RefreshStore
	logger          *slog.Logger
	securityLogger  *slog.Logger
	cookieDomain    string
	cookiePath      string
	cookieSecure    *bool // tri-state: nil = default (true)
	claimsRefresher any   // *ClaimsRefresher[C] erased; auth.go re-types
	leewayOverride  time.Duration
	now             func() time.Time
}

// WithRefreshStore wires the persistence backend for refresh tokens.
// Required for Login/Refresh/Logout handlers; verify-only services can omit it.
func WithRefreshStore(s RefreshStore) Option { return func(o *options) { o.refreshStore = s } }

// WithLogger wires the regular slog logger. Used for handler-level info and
// 5xx mapping. nil = silent.
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

// WithSecurityLogger wires a separate logger for security-relevant anomalies
// (refresh_reused, invalid_token with valid signature but wrong issuer, etc).
// These emit at WARN regardless of HTTP class. nil = no security log.
func WithSecurityLogger(l *slog.Logger) Option { return func(o *options) { o.securityLogger = l } }

// WithCookieDomain pins the refresh cookie's Domain attribute. Empty = host-only.
func WithCookieDomain(d string) Option { return func(o *options) { o.cookieDomain = d } }

// WithCookiePath overrides the default "/auth" cookie path. Useful for services
// mounting the auth subtree under a different prefix.
func WithCookiePath(p string) Option { return func(o *options) { o.cookiePath = p } }

// WithCookieSecure forces the Secure flag on the refresh cookie. Default true.
// Pass false ONLY in local dev over plain HTTP.
func WithCookieSecure(secure bool) Option {
	return func(o *options) { o.cookieSecure = &secure }
}

// WithLeeway overrides the clock-skew leeway used in token verification.
// Empty/zero = use Config.Leeway (which defaults to 1 minute).
func WithLeeway(d time.Duration) Option { return func(o *options) { o.leewayOverride = d } }

// withNow injects a fake clock for tests. Unexported — production code uses
// time.Now exclusively.
func withNow(now func() time.Time) Option { return func(o *options) { o.now = now } }
