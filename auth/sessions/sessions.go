package sessions

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"time"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Mode picks the Middleware's strictness.
type Mode int

const (
	// Optional reads + populates the session if the cookie is
	// present and valid, otherwise falls through (route remains
	// anonymous). Pair with [auth.MustFrom] in handlers that
	// require auth.
	Optional Mode = iota

	// Required returns 401 (CodeMissingSession) when the cookie is
	// absent / expired / invalid.
	Required
)

// Session is the persisted record. Claims is JSON-encoded so the
// SessionStore implementation stays free of the per-service C type
// parameter; the manager re-decodes into the configured C when
// loading.
type Session struct {
	ID         string          `json:"id"`
	Subject    string          `json:"subject"`
	Scopes     []string        `json:"scopes,omitempty"`
	Roles      []string        `json:"roles,omitempty"`
	Claims     json.RawMessage `json:"claims,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	LastSeenAt time.Time       `json:"last_seen_at"`
	ExpiresAt  time.Time       `json:"expires_at"`
}

// SessionStore is the persistence interface. Implementations MUST
// honour ctx.Done() and may be called concurrently from multiple
// goroutines.
//
// Get returns (nil, nil) on miss — the manager treats this as
// "no such session" rather than an error. Other errors propagate
// as CodeStoreFailed.
type SessionStore interface {
	Create(ctx context.Context, sess *Session) error
	Get(ctx context.Context, id string) (*Session, error)
	Touch(ctx context.Context, id string, lastSeen, expires time.Time) error
	Delete(ctx context.Context, id string) error
	// DeleteForSubject removes every active session for subject —
	// the "log out everywhere" operation. Implementations that
	// can't enumerate by subject MAY return an unsupported error.
	DeleteForSubject(ctx context.Context, subject string) error
}

// StoreStats is the rollup returned by [Lister.Stats]:
//
//	Active = ExpiresAt > now AND still present in the store
//	Total  = number of rows the store could enumerate
//
// Note that Total ≠ Active + (rows that have expired) in general:
// auto-evicting backends (Redis EXPIREAT, in-memory stores with
// background GC) drop expired rows before Stats sees them, so the
// "how many expired rows exist?" question is unanswerable
// cross-backend. Use [Lister.ListBySubject] to inspect per-user
// session state if you need to act on expired rows.
type StoreStats struct {
	Active int
	Total  int
}

// Lister is the optional admin-side surface a SessionStore MAY
// implement. The Manager type-asserts on it lazily, so a store that
// can't enumerate (e.g. cookie-only signed sessions) keeps working
// transparently — only [Manager.LogoutEverywhere] downgrades from
// reporting an exact count to -1.
//
// Both the kit's MemoryStore and the sessionsredis.Store implement
// Lister. Use it from admin endpoints to render "active sessions for
// user X" panes / "force-revoke this specific session" buttons.
type Lister interface {
	// ListBySubject returns every session bound to the subject. Both
	// active and expired rows surface; the caller filters via the
	// returned Session.ExpiresAt against time.Now() (the kit favours
	// returning the full picture over hiding state).
	//
	// Empty subject returns an empty slice without backend access.
	ListBySubject(ctx context.Context, subject string) ([]Session, error)

	// Stats returns the Active count + Total enumerable count. Use
	// for /admin or /metrics-pull endpoints.
	Stats(ctx context.Context) (StoreStats, error)
}

// Config tunes a [Manager].
type Config struct {
	// Store is the persistence backend. Required.
	Store SessionStore

	// CookieName is the cookie name. Default "sid".
	CookieName string

	// Domain restricts the cookie to a specific host. Empty omits
	// the Domain attribute (browser uses the request host).
	Domain string

	// Path scope. Default "/".
	Path string

	// InsecureCookie disables the cookie's Secure (HTTPS-only)
	// attribute. Default false — cookies are HTTPS-only by
	// default. Flip to true ONLY for local dev over plain HTTP.
	InsecureCookie bool

	// SameSite controls cross-site cookie behaviour. Default
	// "Lax". Use "Strict" for max isolation, "None" for embedded
	// OAuth callbacks (requires Secure=true).
	SameSite string

	// TTL is the absolute lifetime — the cookie + Session expire
	// no later than CreatedAt + TTL regardless of activity.
	// Required (> 0).
	TTL time.Duration

	// IdleTimeout is the sliding inactivity window — every
	// successful Middleware hit advances ExpiresAt to NOW() +
	// IdleTimeout, capped at CreatedAt + TTL. Default = TTL
	// (effectively disables idle expiry).
	IdleTimeout time.Duration
}

// LogValue mostly suppresses Config from logs (it contains the
// SessionStore — typically wrapping a Redis client).
func (cfg Config) applyDefaults() Config {
	if cfg.CookieName == "" {
		cfg.CookieName = "sid"
	}
	if cfg.Path == "" {
		cfg.Path = "/"
	}
	if cfg.SameSite == "" {
		cfg.SameSite = "Lax"
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = cfg.TTL
	}
	return cfg
}

// validate reports configuration mistakes that would otherwise show
// up at runtime.
func (cfg Config) validate() error {
	if cfg.Store == nil {
		return xerrs.Validation(CodeInvalidConfig, "sessions: Store is required")
	}
	if cfg.TTL <= 0 {
		return xerrs.Validation(CodeInvalidConfig, "sessions: TTL must be > 0")
	}
	return nil
}

// newID returns a 32-byte URL-safe random string suitable as a
// cookie value (RFC 6265 — only [A-Za-z0-9-_] guaranteed safe).
//
// 256 bits of entropy is well above the standard ~128-bit
// session-ID floor and survives the URL-safe base64 round-trip
// without padding.
func newID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// validID reports whether s looks like a kit-issued session ID
// (RawURLEncoding of 32 bytes → 43 chars). Defends against trivial
// tampering before a Store round-trip.
func validID(s string) bool {
	if len(s) != 43 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}
