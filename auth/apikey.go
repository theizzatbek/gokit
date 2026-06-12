package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/auth/internal/principalkey"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// API-key related error Codes. Stable across versions — callers can
// switch on *errs.Error.Code without depending on the underlying
// error type.
const (
	// CodeAPIKeyMissing — no X-API-Key header on the request (and
	// the middleware is not in optional mode).
	CodeAPIKeyMissing = "api_key_missing"

	// CodeAPIKeyInvalid — the key did not match any KeyStore
	// record. Returned for any KeyStore Lookup that surfaces
	// NotFound to suppress key-existence side channels.
	CodeAPIKeyInvalid = "api_key_invalid"

	// CodeAPIKeyExpired — the matching record's ExpiresAt is past.
	CodeAPIKeyExpired = "api_key_expired"

	// CodeAPIKeyRevoked — the matching record has a non-zero
	// RevokedAt.
	CodeAPIKeyRevoked = "api_key_revoked"

	// CodeAPIKeyMissingSecret — Auth was constructed without
	// supplying the HMAC pepper (neither Config.APIKeyHashSecret
	// nor [WithAPIKeyHashSecret]) but the APIKey middleware was
	// invoked. Panics at middleware-build time to surface the
	// misconfig before the first request lands.
	CodeAPIKeyMissingSecret = "api_key_missing_secret"
)

// KeyRecord is the record returned by [KeyStore.Lookup] on a hit.
// Zero ExpiresAt means "no expiry"; zero RevokedAt means
// "not revoked".
//
// The kit projects the record into the same Locals-stored
// [Principal[C]] populated by Bearer middleware so downstream
// scope-check / role-check helpers (RequireScope, MustFrom, …)
// work unchanged regardless of whether the request authenticated
// with a JWT or an API key.
type KeyRecord struct {
	// ID is the public identifier of the key (typically a UUID).
	// Surfaces in Principal.JTI so per-key audit trails can join
	// requests back to the originating key.
	ID string

	// Subject identifies the principal (service name / user id /
	// machine identity). Surfaces in Principal.Subject.
	Subject string

	// Scopes are the authorisation scopes this key carries.
	// Surface in Principal.Scopes and gate downstream
	// [Auth.RequireScopeFactory] / RequireScope checks.
	Scopes []string

	// Role is the broad role (admin / user / …) the key has.
	// Surfaces in Principal.Roles as a single-element slice.
	// Optional.
	Role string

	// ExpiresAt — past = expired. Zero = no expiry.
	ExpiresAt time.Time

	// RevokedAt — non-zero = revoked. Zero = active.
	RevokedAt time.Time
}

// KeyStore is the persistence backend the APIKey middleware
// consults for every request. Lookup MUST return a *KeyRecord on
// hit or a *errs.Error{Kind: NotFound} on miss; other errors flow
// through unchanged (the middleware turns NotFound into 401 with
// [CodeAPIKeyInvalid] and other errors into 503).
//
// [auth/apikeypg.NewStore] is the kit's Postgres-backed default.
// Roll your own for non-pg backends (in-memory for tests, KMS-
// backed for short-TTL service keys, etc).
type KeyStore interface {
	Lookup(ctx context.Context, keyHash []byte) (*KeyRecord, error)
}

// KeyUsageTracker is the optional audit hook KeyStore implementations
// MAY satisfy to track API-key usage. When the kit detects a Lookup
// hit AND the store also implements KeyUsageTracker, it fires
// MarkUsed in a fresh background goroutine — the hot path stays
// allocation-free and never waits on a DB round trip.
//
// `id` is the KeyRecord.ID returned by Lookup; `t` is the wall clock
// at the request. Implementations typically `UPDATE api_keys SET
// last_used_at = $2 WHERE id = $1 AND ($2 - last_used_at) > '1m'` to
// throttle write pressure under bursty load.
//
// A non-nil error from MarkUsed surfaces only in implementor logs —
// the kit deliberately discards it (failure to update an audit
// timestamp is never worth rejecting an authenticated request).
type KeyUsageTracker interface {
	MarkUsed(ctx context.Context, id string, t time.Time) error
}

// APIKeyOption tunes [Auth.APIKey].
type APIKeyOption func(*apiKeyConfig)

// APIKeyAuthSuccessHook fires after a successful API-key authentication,
// AFTER the Principal is stashed in Locals but BEFORE the next handler
// runs. Use for audit trails, Sentry user-scope wiring, request-id
// tagging, etc.
//
// Panic-safe — a panicking hook is recovered + logged via the kit
// logger (when [WithLogger] was wired); the request still proceeds
// normally. Use the *fiber.Ctx for IP/path/headers; the flat
// subject/jti/scopes/roles fields avoid leaking the generic Principal[C]
// type through the option signature.
type APIKeyAuthSuccessHook func(c *fiber.Ctx, subject, jti string, scopes, roles []string)

// APIKeyAuthFailHook fires on every API-key reject path (missing /
// invalid / expired / revoked / KeyStore error), BEFORE the 401 / 5xx
// response leaves the wrapper. `code` is the stable [CodeAPIKey*]
// constant so callers can switch on it without parsing log messages.
//
// Panic-safe — same convention as [APIKeyAuthSuccessHook].
type APIKeyAuthFailHook func(c *fiber.Ctx, code string)

type apiKeyConfig struct {
	headerName  string
	queryName   string
	optional    bool
	keyHashFunc func(key string, secret []byte) []byte
	onSuccess   APIKeyAuthSuccessHook
	onFail      APIKeyAuthFailHook
}

// WithAPIKeyHeader overrides the inbound header name. Default
// "X-API-Key" (matches GitHub / Vercel / most SaaS conventions).
// The lookup is case-insensitive (fiber's Get).
func WithAPIKeyHeader(name string) APIKeyOption {
	return func(c *apiKeyConfig) { c.headerName = name }
}

// WithAPIKeyQuery enables a query-string fallback (useful for
// webhooks that can only set query params). Default disabled.
// Pass `"api_key"` to allow `?api_key=...`. Header always wins
// when both are present.
func WithAPIKeyQuery(name string) APIKeyOption {
	return func(c *apiKeyConfig) { c.queryName = name }
}

// WithAPIKeyOptional flips the middleware into "may be anonymous"
// mode: missing key → pass through without principal. Use when a
// route serves both API-key authenticated callers AND anonymous
// clients (mixed public/private endpoints).
//
// A PRESENT-but-invalid key is still rejected — never silently
// downgrade a forged key to anonymous.
func WithAPIKeyOptional() APIKeyOption {
	return func(c *apiKeyConfig) { c.optional = true }
}

// WithAPIKeyOnSuccess registers a callback fired after every successful
// API-key authentication. See [APIKeyAuthSuccessHook] for semantics.
// Multiple calls — last wins (the option list is reduced into a single
// apiKeyConfig before middleware build).
func WithAPIKeyOnSuccess(fn APIKeyAuthSuccessHook) APIKeyOption {
	return func(c *apiKeyConfig) { c.onSuccess = fn }
}

// WithAPIKeyOnFail registers a callback fired on every API-key reject
// path. See [APIKeyAuthFailHook] for semantics. Multiple calls — last
// wins.
func WithAPIKeyOnFail(fn APIKeyAuthFailHook) APIKeyOption {
	return func(c *apiKeyConfig) { c.onFail = fn }
}

// APIKey returns a Fiber middleware that authenticates inbound
// requests via the `X-API-Key` header (overridable). Workflow:
//
//  1. Extract the raw key from the configured header (and optional
//     query param).
//  2. Hash via HMAC-SHA256 with the kit secret supplied at
//     `Auth` construction time via [WithAPIKeyHashSecret]. The
//     hash IS the lookup key — KeyStore implementations never see
//     the raw key, only the HMAC.
//  3. Call `KeyStore.Lookup(ctx, hash)`.
//  4. Check ExpiresAt / RevokedAt.
//  5. Build a [Principal[C]] (with zero-value C for custom claims),
//     store under the same `principalkey.Key{}` slot Bearer uses, call
//     c.Next().
//
// 401 + WWW-Authenticate challenge on every reject path so
// clients see a consistent failure mode regardless of the
// underlying cause (missing / expired / revoked / unknown).
//
// Panics at construction time with [CodeAPIKeyMissingSecret] when
// the kit-side hashing secret has not been wired — this is a
// programmer-error, not a runtime one.
func (a *Auth[C]) APIKey(store KeyStore, opts ...APIKeyOption) fiber.Handler {
	if len(a.apiKeyHashSecret) == 0 {
		panic(xerrs.Internal(CodeAPIKeyMissingSecret,
			"auth: APIKey middleware requires auth.Config.APIKeyHashSecret"))
	}
	if store == nil {
		panic(xerrs.Internal(CodeAPIKeyMissingSecret,
			"auth: APIKey middleware requires a non-nil KeyStore"))
	}
	cfg := &apiKeyConfig{
		headerName:  "X-API-Key",
		keyHashFunc: hmacKeyHash,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	secret := a.apiKeyHashSecret
	return func(c *fiber.Ctx) error {
		raw := extractAPIKey(c, cfg)
		if raw == "" {
			if cfg.optional {
				return c.Next()
			}
			err := xerrs.Unauthorized(CodeAPIKeyMissing,
				"missing "+cfg.headerName+" header")
			a.metrics.incAPIKeyAuth("missing")
			a.maybeSecurityLog(c, "apikey_missing", err)
			a.fireAPIKeyFail(c, CodeAPIKeyMissing, cfg.onFail)
			return apiKeyReject(c, err)
		}
		hash := cfg.keyHashFunc(raw, secret)
		start := time.Now()
		rec, err := store.Lookup(c.UserContext(), hash)
		a.metrics.observeAPIKeyLookup(time.Since(start).Seconds())
		if err != nil {
			// Suppress NotFound → 401 (same shape as missing key
			// to deny existence side channels). Other errors flow
			// through with their original Kind so 503 stays 503.
			var e *xerrs.Error
			if errors.As(err, &e) && e.Kind == xerrs.KindNotFound {
				invalid := xerrs.Unauthorized(CodeAPIKeyInvalid,
					"API key not recognised")
				a.metrics.incAPIKeyAuth("invalid")
				a.maybeSecurityLog(c, "apikey_invalid", invalid)
				a.fireAPIKeyFail(c, CodeAPIKeyInvalid, cfg.onFail)
				return apiKeyReject(c, invalid)
			}
			a.metrics.incAPIKeyAuth("error")
			a.maybeSecurityLog(c, "apikey_lookup_error", err)
			a.fireAPIKeyFail(c, "apikey_lookup_error", cfg.onFail)
			return err
		}
		if !rec.RevokedAt.IsZero() {
			revoked := xerrs.Unauthorized(CodeAPIKeyRevoked,
				"API key has been revoked")
			a.metrics.incAPIKeyAuth("revoked")
			a.maybeSecurityLog(c, "apikey_revoked", revoked)
			a.fireAPIKeyFail(c, CodeAPIKeyRevoked, cfg.onFail)
			return apiKeyReject(c, revoked)
		}
		if !rec.ExpiresAt.IsZero() && time.Now().After(rec.ExpiresAt) {
			expired := xerrs.Unauthorized(CodeAPIKeyExpired,
				"API key expired")
			a.metrics.incAPIKeyAuth("expired")
			a.maybeSecurityLog(c, "apikey_expired", expired)
			a.fireAPIKeyFail(c, CodeAPIKeyExpired, cfg.onFail)
			return apiKeyReject(c, expired)
		}
		principal := recordToPrincipal[C](rec)
		c.Locals(principalkey.Key{}, principal)
		a.metrics.incAPIKeyAuth("success")
		a.maybeSecurityInfo(c, "apikey_auth_success",
			"subject", principal.Subject, "jti", principal.JTI)
		a.fireAPIKeySuccess(c, principal, cfg.onSuccess)
		if tracker, ok := store.(KeyUsageTracker); ok && rec.ID != "" {
			id := rec.ID
			now := time.Now()
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = tracker.MarkUsed(ctx, id, now)
			}()
		}
		return c.Next()
	}
}

// fireAPIKeySuccess invokes the success hook under a recover. The
// recovered panic is logged via the kit logger when one is wired —
// otherwise the panic is swallowed so a misbehaving hook can never
// take down the request path.
func (a *Auth[C]) fireAPIKeySuccess(c *fiber.Ctx, p *Principal[C], fn APIKeyAuthSuccessHook) {
	if fn == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && a.logger != nil {
			a.logger.WarnContext(c.UserContext(), "auth.APIKey: OnSuccess panic recovered",
				"panic", r, "subject", p.Subject)
		}
	}()
	fn(c, p.Subject, p.JTI, p.Scopes, p.Roles)
}

// fireAPIKeyFail invokes the failure hook under a recover. Same
// convention as [fireAPIKeySuccess].
func (a *Auth[C]) fireAPIKeyFail(c *fiber.Ctx, code string, fn APIKeyAuthFailHook) {
	if fn == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && a.logger != nil {
			a.logger.WarnContext(c.UserContext(), "auth.APIKey: OnFail panic recovered",
				"panic", r, "code", code)
		}
	}()
	fn(c, code)
}

// extractAPIKey reads the raw key from header or (when enabled)
// query string. Header wins when both are present.
func extractAPIKey(c *fiber.Ctx, cfg *apiKeyConfig) string {
	if v := strings.TrimSpace(c.Get(cfg.headerName)); v != "" {
		return v
	}
	if cfg.queryName != "" {
		if v := strings.TrimSpace(c.Query(cfg.queryName)); v != "" {
			return v
		}
	}
	return ""
}

// apiKeyReject sets the RFC 6750-style ApiKey challenge and
// returns the error so the application's ErrorHandler renders it.
func apiKeyReject(c *fiber.Ctx, err error) error {
	code := CodeAPIKeyInvalid
	if x, ok := err.(*xerrs.Error); ok {
		code = x.Code
	}
	c.Set(fiber.HeaderWWWAuthenticate, `ApiKey realm="api", error="`+code+`"`)
	return err
}

// hmacKeyHash is the default key-hash function: HMAC-SHA256 with
// the kit-side secret. Yields a 32-byte indexable hash suitable
// for direct equality lookup in any KV / column store. Constant-
// time inside HMAC's verify path is not needed here since the
// hash itself is the lookup key (no string-compare on the raw key
// happens after this).
func hmacKeyHash(key string, secret []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(key))
	return m.Sum(nil)
}

// HashAPIKey is the exported version of the kit's HMAC-SHA256
// key-hash function, surfaced for KeyStore implementations that
// need to compute the same hash at INSERT time (e.g. an admin
// route minting new keys: hash the plain key once, store the
// HMAC, hand the plain key back to the caller).
//
// Callers MUST use the same secret the kit uses for verification
// (Config.APIKeyHashSecret) — rotating the secret invalidates
// every stored hash.
func HashAPIKey(plain string, secret []byte) []byte {
	return hmacKeyHash(plain, secret)
}

// recordToPrincipal projects a KeyRecord into the kit's standard
// Principal[C] shape. Zero-value C — API keys don't carry the
// custom claims a JWT does.
func recordToPrincipal[C any](r *KeyRecord) *Principal[C] {
	var zero C
	roles := []string(nil)
	if r.Role != "" {
		roles = []string{r.Role}
	}
	return &Principal[C]{
		Subject:  r.Subject,
		Issuer:   "api_key",
		Audience: nil,
		IssuedAt: time.Time{},
		Expires:  r.ExpiresAt,
		JTI:      r.ID,
		Scopes:   r.Scopes,
		Roles:    roles,
		Claims:   zero,
		Raw:      "", // intentional: never echo the raw key
	}
}

// APIKeyFactory adapts [Auth.APIKey] to the fibermap middleware-
// factory contract for YAML-declared routes.
//
//	middleware:
//	  - api_key: []           # default header, required
//	  - api_key: ["optional"] # allow anonymous fallback
//
// The store is bound once at fibermount-mount time via
// [fibermount.MountAPIKeyFactory]; per-route YAML args only flip
// the `optional` mode for now (header / query overrides stay
// programmatic, not declarative — declarative tends to invite
// inconsistent header names across routes).
func (a *Auth[C]) APIKeyFactory(store KeyStore) func([]any) (fiber.Handler, error) {
	return func(args []any) (fiber.Handler, error) {
		var opts []APIKeyOption
		for _, raw := range args {
			s, ok := raw.(string)
			if !ok {
				return nil, xerrs.Validationf(CodeAPIKeyMissingSecret,
					"api_key: arg must be string, got %T", raw)
			}
			switch s {
			case "optional":
				opts = append(opts, WithAPIKeyOptional())
			default:
				return nil, xerrs.Validationf(CodeAPIKeyMissingSecret,
					"api_key: unknown arg %q (allowed: \"optional\")", s)
			}
		}
		return a.APIKey(store, opts...), nil
	}
}
