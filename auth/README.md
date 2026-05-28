# auth

JWT issue/verify (asymmetric EdDSA/ES256), generic `Claims[C]` for app-specific custom data, argon2id password hashing, pluggable refresh-token storage, and ready-to-mount Fiber middleware/handlers (Bearer, RequireScope, RequireRole, Login, Refresh, Logout).

**Import:** `github.com/theizzatbek/gokit/auth`
**Depends on:** `golang-jwt/jwt/v5`, `golang.org/x/crypto/argon2`, `gofiber/fiber/v2`, `github.com/theizzatbek/gokit/errs`

## Why use it

Auth is the single hand-rolled boilerplate every service repeats: JWT key management, password hashing parameters, refresh-token rotation policy, Bearer middleware, login/refresh/logout HTTP handlers. `auth` ships the whole bundle with sensible defaults (Ed25519 + argon2id), a pluggable refresh store (so you choose Postgres/Redis), and zero coupling to the rest of the kit — `auth` does NOT import `db` or `redis`.

## Quickstart

```go
import (
    "context"
    "time"
    "github.com/theizzatbek/gokit/auth"
    "github.com/theizzatbek/gokit/auth/refreshpg"
)

// 1. Load key material (from PEM env var, secret manager, etc.)
keySet, err := auth.LoadKeysFromPEM("k1", map[string][]byte{
    "k1": []byte(pemPrivateKey),
})

// 2. Construct *Auth[YourClaims]
type MyClaims struct {
    Email string `json:"email"`
}
authObj, err := auth.New[MyClaims](auth.Config{
    Issuer:     "myservice",
    Keys:       keySet,
    AccessTTL:  15 * time.Minute,
    RefreshTTL: 30 * 24 * time.Hour,
}, auth.WithRefreshStore(refreshpg.New(db)))

// 3. Write your own login handler. The kit does not own the body shape — you
//    declare LoginRequest yourself, verify credentials however you like
//    (password, mTLS, PKCS7, OIDC, magic link), and hand the verified
//    LoginResult to IssueLogin. The kit mints + persists tokens and writes
//    the {access_token, ...} response.
type LoginRequest struct {
    Login    string `json:"login"    validate:"required"`
    Password string `json:"password" validate:"required,min=1"`
}

app.Post("/auth/login", func(c *fiber.Ctx) error {
    var req LoginRequest
    if err := c.BodyParser(&req); err != nil {
        return errs.Wrap(err, errs.KindValidation, "invalid_body", "could not decode body")
    }
    u, err := usersSvc.Authenticate(c.UserContext(), req.Login, req.Password)
    if err != nil {
        return err
    }
    return authObj.IssueLogin(c, auth.LoginResult[MyClaims]{
        Subject: u.ID,
        Custom:  MyClaims{Email: u.Email},
    })
})

// 4. Refresh and logout are one-liners — they have no service-side logic.
app.Post("/auth/refresh", authObj.IssueRefresh)
app.Post("/auth/logout",  authObj.Logout)

// 5. Protect routes
app.Use(authObj.Bearer(auth.BearerRequired))
app.Get("/me", func(c *fiber.Ctx) error {
    p, err := auth.MustFrom[MyClaims](c)
    if err != nil { return err }
    return c.JSON(p.Claims)
})
```

### Custom auth schemes

`IssueLogin` only cares about the verified `LoginResult[C]` — it doesn't see
the wire body. Custom schemes (mTLS, PKCS7-signed payloads, OIDC id_token,
SAML, SSH-cert, magic links) all follow the same pattern:

```go
app.Post("/auth/login-cert", func(c *fiber.Ctx) error {
    sig, err := parsePKCS7(c.Body())               // your verification
    if err != nil { return errs.Unauthorized(...) }
    subject := extractSubject(sig.Certificate())   // your subject mapping
    return authObj.IssueLogin(c, auth.LoginResult[MyClaims]{
        Subject: subject,
        Custom:  MyClaims{...},
    })
})
```

For non-Fiber callers (RPC handlers, CLI tools, background jobs), use
`IssueTokens(ctx, res, meta)` / `RotateRefresh(ctx, raw, meta)` directly —
they return a `TokenPair` and never touch `*fiber.Ctx`.

## Configuration

### `auth.Config`

| Field | Default | Notes |
|---|---|---|
| `Issuer` | — | Required. Goes into JWT `iss`. |
| `Audience` | nil | Optional `aud` allowlist; nil = no audience check |
| `Keys` | — | Required. `*KeySet` — see "Key management" below |
| `AccessTTL` | — | Required (e.g. 15m). Access-token lifetime |
| `RefreshTTL` | — | Required (e.g. 30 * 24h). Refresh-token lifetime |
| `Leeway` | 1 minute | Clock-skew tolerance during `exp`/`nbf` validation |

### Options

| Option | Default | Notes |
|---|---|---|
| `WithRefreshStore(RefreshStore)` | none | Required for Login/Refresh/Logout. Plug `refreshpg`/`refreshredis`/your own |
| `WithLogger(*slog.Logger)` | silent | App-level errors |
| `WithSecurityLogger(*slog.Logger)` | silent | Security-relevant events: refresh reuse, token revocation, login failures |
| `WithCookieDomain(d)`, `WithCookiePath(p)` | "" / "/" | Refresh-cookie scope |
| `WithCookieSecure(bool)` | true | Force/disable `Secure` flag on refresh cookie |
| `WithLeeway(d)` | from Config.Leeway | Override leeway after construction |

## Key management

`*KeySet` carries the active signing key + a map of verify-only keys for rotation. Build one via:

```go
// From PEM bytes (production)
ks, err := auth.LoadKeysFromPEM("k1", map[string][]byte{
    "k1": pemActive,
    "k0": pemOld,       // still verify tokens signed under "k0", don't sign new ones
})

// Generate in-process (for tests + key bootstrap)
ks, err := auth.GenerateEd25519Key("k1")
```

Active key kid goes into the JWT `kid` header → verify-only services with the public-key-only PEM can validate without holding signing material. Rotation is non-breaking: deploy verifiers with both old + new public keys first, then flip the active key.

## Common patterns

### Bearer middleware modes

```go
// Required (default) — missing or invalid token → 401
app.Use(authObj.Bearer(auth.BearerRequired))

// Optional — missing token = anonymous pass-through, invalid token = still 401
app.Use(authObj.Bearer(auth.BearerOptional))
```

**Important:** if you also build a fibermap engine, install `auth.BearerOptional` at the fiber.App level via `fibermap.WithUse(...)` so it runs BEFORE the engine's contextInit (which often reads the principal from Locals). Per-route enforcement uses the `bearer: []` factory middleware from `auth/fibermount`.

### Inspecting the authenticated principal

```go
// Returns ("", false) if no principal
if p, ok := auth.From[MyClaims](c); ok {
    fmt.Println(p.Subject, p.Custom.Email)
}

// Fails with *errs.Error{KindUnauthorized} if missing — use after BearerRequired
p, err := auth.MustFrom[MyClaims](c)

// Convenience accessors
subject := auth.Subject[MyClaims](c)         // "" when no principal
allowed := auth.HasScope[MyClaims](c, "admin:write")
```

### Password hashing

```go
hasher := auth.NewHasher(auth.DefaultParams())
hash, err := hasher.Hash("user-password")
if err := hasher.Verify(hashFromDB, "user-password"); err != nil {
    // mismatch — returns *errs.Error{KindUnauthorized}
}
// Re-hash on next successful login if params changed:
if hasher.NeedsRehash(hashFromDB) { /* re-hash and store */ }
```

`auth.DefaultParams()` is OWASP-recommended argon2id (memory 64MB, iterations 3, parallelism 4). Override with `auth.NewHasher(auth.Params{...})` if you need slower-for-secrecy vs faster-for-throughput.

### Custom claims refresh on /auth/refresh

```go
authObj.SetClaimsRefresher(func(ctx context.Context, subject string) (auth.LoginResult[MyClaims], error) {
    u, err := usersSvc.ByID(ctx, subject)   // re-read current user state
    if err != nil { return auth.LoginResult[MyClaims]{}, err }
    return auth.LoginResult[MyClaims]{
        Subject: u.ID,
        Scopes:  u.Scopes,                  // pick up role changes since login
        Custom:  MyClaims{Email: u.Email},
    }, nil
})
```

Without `SetClaimsRefresher`, refreshed access tokens carry only the rotated record's Subject and empty Scopes/Roles/Custom.

### Refresh-token rotation + reuse detection

Refresh tokens are single-use. `auth.IssueRefresh` (or `RotateRefresh` for
non-Fiber callers):
1. Consumes the presented token (atomic UPDATE in `refreshpg`, Lua script in `refreshredis`).
2. If already consumed → **revokes the entire family** and returns `*errs.Error{Code: "refresh_reused"}`. This catches replay attacks.
3. On success, issues a new (access, refresh) pair from the same family.

Set `WithSecurityLogger(...)` to receive a structured WARN every time a reuse triggers a family revoke — a useful alert signal.

## Error model

| Path | Error |
|---|---|
| Custom login handler invalid creds | `*errs.Error{KindUnauthorized, Code: "invalid_credentials"}` (returned by your code before reaching `IssueLogin`) |
| Custom login handler bad body | `*errs.Error{KindValidation, Code: "invalid_body"}` (whatever your handler returns; the kit does not parse) |
| `IssueRefresh` missing/expired token | `*errs.Error{KindUnauthorized, Code: "missing_refresh"}` / `"refresh_expired"` |
| `IssueRefresh` reuse detected | `*errs.Error{KindUnauthorized, Code: "refresh_reused"}` + family revoked |
| `IssueTokens` / `IssueLogin` / `IssueRefresh` no store | `*errs.Error{KindInternal, Code: "store_unset"}` |
| Store backend unreachable | `*errs.Error{KindUnavailable, Code: "store_unavailable"}` |
| `Bearer` missing token (required) | `*errs.Error{KindUnauthorized, Code: "missing_token"}` |
| `Bearer` invalid/expired token | `*errs.Error{KindUnauthorized, Code: "invalid_token"}` |
| `NewHasher` invalid params | error from `validateParams()` |
| `Hash` / `Verify` failure | `*errs.Error{KindInternal}` or `KindUnauthorized` on mismatch |

All errors land in your `fibermap.ErrorHandler` and emit the standard wire shape.

## Wire shapes

### POST /auth/login

```json
// Request
{"login": "a@b.com", "password": "..."}

// Response 200
{
  "access_token": "<JWT>",
  "token_type":   "Bearer",
  "expires_in":   900,
  "subject":      "user-uuid"
}
// Plus: refresh_token cookie (HttpOnly, Secure, SameSite=Strict by default)
```

### POST /auth/refresh

Reads the `refresh_token` cookie. Returns the same JSON shape as `/auth/login`. Sets a NEW refresh-token cookie.

### POST /auth/logout / /auth/logout/all

Revokes the current token (or the entire family). Returns 204. Clears the `refresh_token` cookie.

## Observability

- `WithLogger(*slog.Logger)` — INFO on issue/refresh, WARN on issuance errors, ERROR on signature failures
- `WithSecurityLogger(*slog.Logger)` — separate stream for security-relevant events (failed logins, reuse-triggered revocations, missing tokens). Wire to your SIEM.

## Testing

For integration with a real refresh store, use the per-store testcontainers fixtures (`auth/refreshpg/store_test.go::initPostgresContainer`, `auth/refreshredis/store_test.go::initRedisContainer`).

For unit tests of handlers that take `*auth.Auth[C]`, generate keys in-process:

```go
ks, _ := auth.GenerateEd25519Key("test")
authObj, _ := auth.New[MyClaims](auth.Config{
    Issuer: "test", Keys: ks, AccessTTL: time.Minute, RefreshTTL: time.Hour,
}, auth.WithRefreshStore(refreshmem.New()))  // or refreshpg/refreshredis
```

## Limitations

- **No OAuth/OIDC** — bring your own provider integration; `auth` is for first-party credentials.
- **No multi-factor** out of the box. Add a second middleware that requires a separate factor claim.
- **No session storage** — JWTs are stateless. Use Bearer + refresh rotation; if you need server-side session revocation per-access-token, switch to opaque tokens (out of scope here).
- **Refresh cookie is browser-targeted.** Mobile/API clients should consume the cookie value or the kit needs a tweak — the cookie path isn't optional.
- **Argon2id memory ≈ 64MB per concurrent hash.** Provision accordingly; tune `Params` if memory-constrained.

## See also

- [`auth/refreshpg`](refreshpg/README.md) — Postgres-backed `RefreshStore`
- [`auth/refreshredis`](refreshredis/README.md) — Redis-backed `RefreshStore`
- [`auth/fibermount`](fibermount/README.md) — one-call mount of `bearer`/`require_scope`/`require_role` factories into a fibermap engine
- [`errs`](../errs/README.md) — error model used everywhere
- [`examples/urlshort`](../examples/urlshort/README.md) — register → login → refresh → Bearer-protected routes
