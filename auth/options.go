package auth

import (
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Option configures New beyond what Config covers.
type Option func(*options)

type options struct {
	refreshStore             RefreshStore
	revokedAccess            RevokedAccessStore
	logger                   *slog.Logger
	securityLogger           *slog.Logger
	metrics                  prometheus.Registerer
	cookieDomain             string
	cookiePath               string
	cookieSecure             *bool // tri-state: nil = default (true)
	leewayOverride           time.Duration
	now                      func() time.Time
	ipExtractor              IPExtractor
	apiKeyHashSecretOverride []byte
}

// WithRefreshStore wires the persistence backend for refresh tokens.
// Required for Login/Refresh/Logout handlers; verify-only services can omit it.
func WithRefreshStore(s RefreshStore) Option { return func(o *options) { o.refreshStore = s } }

// WithLogger wires the regular slog logger. Used for handler-level info and
// 5xx mapping. nil = silent.
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

// WithSecurityLogger wires a separate logger for security-relevant events:
//
//	WARN — bearer_verify_failed, refresh_reused (anomalies, errors attached)
//	INFO — login_success, logout, logout_all   (subject, ip, ua, path attached)
//
// All events include ip / ua / path; INFO events also include the
// authenticated subject. SIEM / detection-rule consumers should key off
// the `msg` field on the JSON line. nil = no security log.
func WithSecurityLogger(l *slog.Logger) Option { return func(o *options) { o.securityLogger = l } }

// WithMetrics enables Prometheus instrumentation. Auth registers the
// following series on reg:
//
//   - auth_tokens_issued_total{op}              login | refresh
//   - auth_token_issue_failed_total{op,reason}  store | sign
//   - auth_bearer_verify_total{outcome}         ok | invalid
//   - auth_refresh_total{outcome}               ok | reused | expired | invalid | missing
//   - auth_logout_total{scope}                  single | all
//   - auth_ratelimit_denied_total
//   - auth_idempotency_total{outcome}           hit | miss | skip
//   - auth_apikey_authentications_total{outcome} success | missing | invalid | expired | revoked | error
//   - auth_apikey_lookup_duration_seconds       histogram, KeyStore.Lookup wall-clock
//
// Pass the same Registerer you give to db/httpc/nats so a single
// /metrics scrape covers the whole kit. Without this option auth runs
// without metrics — the instrumentation hooks no-op in O(1) (nil
// guard, no labels resolved).
//
// To get rate-limit / idempotency counters on YAML-mounted middleware
// you must additionally register Auth-bound factories — see
// auth/fibermount.
func WithMetrics(reg prometheus.Registerer) Option {
	return func(o *options) { o.metrics = reg }
}

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

// WithAPIKeyHashSecret supplies the HMAC pepper the APIKey middleware
// uses to derive `keyHash` from the plain key before calling
// KeyStore.Lookup. Functionally equivalent to setting
// Config.APIKeyHashSecret — the Option form is the canonical place
// callers reach for it, mirroring the With* family used for every
// other tunable.
//
// Precedence: this Option WINS over Config.APIKeyHashSecret when
// both are set. Pass a non-empty slice to override; pass nil / empty
// to fall back to Config (or to leave Auth without a pepper, which
// will trip CodeAPIKeyMissingSecret if APIKey middleware is built).
//
// 32 bytes minimum is the kit's recommended length (HMAC-SHA256
// best practice). Auth does not enforce the floor at construction
// time — the failure surfaces only when APIKey middleware would
// run — but callers wiring this via service.AuthConfig.APIKeyHashSecret
// will see a service.CodeAuthInvalidAPIKeyHashSecret validation
// error at service.New time when the decoded value is shorter.
func WithAPIKeyHashSecret(secret []byte) Option {
	return func(o *options) {
		o.apiKeyHashSecretOverride = secret
	}
}
