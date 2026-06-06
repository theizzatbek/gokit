# auth

JWT issue/verify, sessions, refresh-token persistence, API-keys, Fiber middleware.

## `auth/`

JWT issue/verify (asymmetric EdDSA/ES256), generic `Claims[C]`, argon2id `Hasher`, refresh-token interface, and ready-to-mount middleware (Bearer / RequireScope / RequireRole / RequireAnyScope / RequireAnyRole) + handlers (Login/Refresh/Logout). `auth.New[C](Config, ...Option) *Auth[C]` is the entry point. Core package depends on stdlib + crypto + `golang-jwt/jwt/v5` + fiber + `errs/`; deliberately does NOT import `db/` or `redis`. Refresh persistence is pluggable via the `RefreshStore` interface.

Access tokens carry `kid` in the header for rotation; `KeySet.LoadKeysFromPEM` accepts a mix of private/public PEMs so verify-only services can be wired without signing material. `KeySet.JWKS() ([]byte, error)` renders the verify set as RFC 7517 (EdDSA → `kty=OKP/crv=Ed25519/x`; ES256 → `kty=EC/crv=P-256/x,y`); `Auth.JWKSHandler(maxAge int)` serves `/.well-known/jwks.json` with Cache-Control. `Auth.RotateKeys(*KeySet) error` hot-swaps the signing material under concurrent Sign/Verify (atomic.Pointer; no lock; verify accepts every alg present in the new set so mixed-alg rotation works).

`WithRevokedAccessStore(s RevokedAccessStore)` opt-in JTI blacklist consulted by Bearer after JWT verify; fail-OPEN on backend error so a transient outage doesn't lock out every user. `Auth.RevokeAccess(ctx, Claims[C])` is the admin write path; stable code `token_revoked`. `KeyStore` implementations MAY also satisfy `KeyUsageTracker.MarkUsed(ctx, id, t)` — APIKey middleware type-asserts once and fires the update in a background goroutine (5s ctx) so the hot path stays allocation-free.

**APIKey observability + hooks:** `WithMetrics` now also emits `auth_apikey_authentications_total{outcome=success|missing|invalid|expired|revoked|error}` + `auth_apikey_lookup_duration_seconds` histogram; `WithSecurityLogger` emits `apikey_auth_success` (INFO with subject/jti) and `apikey_missing|invalid|expired|revoked|lookup_error` (WARN with err); per-middleware `WithAPIKeyOnSuccess(func(*fiber.Ctx, subject, jti string, scopes, roles []string))` + `WithAPIKeyOnFail(func(*fiber.Ctx, code string))` are panic-safe lifecycle hooks for audit / Sentry user-scope wiring (panic recovered + logged via `WithLogger`).

`WithIPExtractor(fn IPExtractor)` overrides `c.IP()` Auth-wide for refresh-token meta, security log, and rate-limit fallback bucket; empty return falls back to fiber's stdlib `c.IP()`; `Auth.RateLimit` / `RateLimitBySubject` route their keyers through the same extractor so CDN headers (`CF-Connecting-IP`, `X-Real-IP`, `Fly-Client-IP`) reach the limiter buckets too.

## `auth/refreshpg/`

Postgres-backed `RefreshStore` over `db.Querier`. DDL lives in `auth/refreshpg/schema.sql`; the package does not run migrations. Atomic `Consume` is one `UPDATE ... RETURNING` followed by a diagnostic `SELECT` on the miss path; reuse detection triggers a family-wide `RevokeFamily` before returning the `refresh_reused` error.

**Admin / operator API** (on `*Store` outside the RefreshStore interface): `ListBySubject(ctx, subject) ([]SessionInfo, error)` (history sessions ordered `issued_at DESC` — active/consumed/revoked/expired, UI filters by `State`); `Stats(ctx) (StoreStats{Active, Consumed, Revoked, Expired, Total}, error)` disjoint buckets in one round trip; `RevokeByIP(ctx, ip) (int64, error)` bulk-revokes every active token issued from the IP (idempotent, empty/unknown returns 0); `GarbageCollectBatch(ctx, now, limit, maxIterations) (int64, error)` chunked DELETE for very large tables (`limit ≤ 0` → 1000, `maxIterations ≤ 0` → 1024; ctx cancel returns partial progress). `SessionInfo` never carries `token_hash` — secret material stays in the store.

**Observability + hooks** via `New(d, ...Option)`: `WithMetrics(reg)` registers `refreshpg_ops_total{op,outcome}` (issue/consume/revoke_family/revoke_subject/revoke_ip/gc/stats/list; outcome=ok|error, consume also missing|expired|reused) + `refreshpg_op_duration_seconds{op}` histogram; `WithLogger(slog)` is silent except for hook panic-recovery; `WithOnConsumeReused(fn)` fires INSIDE Consume after `RevokeFamily` already ran — the OAuth 2.1 stolen-token signal; `WithOnFamilyRevoke` / `WithOnSubjectRevoke` / `WithOnIPRevoke` are post-revoke audit hooks. All hooks panic-safe (recovered + WARN-logged via `WithLogger`). Zero-option `New(d)` stays back-compat.

## `auth/refreshredis/`

Redis-backed `RefreshStore` over `redis/go-redis/v9`. Each record is one HASH with `EXPIREAT`; family + subject SETs back the bulk-revoke paths. `Consume` runs as a single Lua script for atomicity (`Consume + reuse detection + family revoke` all server-side).

**IP-revoke index:** Issue now also populates a `refresh:ip:{ip}` auxiliary SET (when `r.IP != ""`) that backs `RevokeByIP`; tokens issued before this feature shipped do not appear in the index — for retroactive sweep run `Stats`/`ListBySubject` from operator scripts.

**Admin / operator API** (mirror of refreshpg): `ListBySubject(ctx, subject) ([]SessionInfo, error)` backed by `refresh:subject:{subject}` + pipelined HGETALL (stale set members whose hash has already been EXPIREATd are silently skipped); `Stats(ctx) (StoreStats, error)` is O(N) — `SCAN refresh:*` excluding aux sets + pipelined HMGET, for admin/diagnostic use only (EXPIREATd records are invisible — Redis dropped them); `RevokeByIP(ctx, ip) (int64, error)` consults the auxiliary set + pipelined `HSET revoked=1`. `SessionInfo.ConsumedAt`/`RevokedAt` are sentinel timestamps (zero = false, non-zero = the boolean flag is set; Redis-side only the flags are persisted, not exact timestamps).

**GarbageCollect** now also sweeps `refresh:ip:*` stale members alongside family/subject sets.

**Observability + hooks** via `New(rdb, ...Option)`: `WithMetrics(reg)` → `refreshredis_ops_total{op,outcome}` + `refreshredis_op_duration_seconds{op}` (op: issue/consume/revoke_family/revoke_subject/revoke_ip/gc/stats/list); `WithLogger`, `WithOnConsumeReused`, `WithOnFamilyRevoke`, `WithOnSubjectRevoke`, `WithOnIPRevoke` — same semantics as the refreshpg side, all panic-safe. Zero-option `New(rdb)` stays back-compat.

## `auth/apikeypg/`

Postgres-backed `auth.KeyStore` over `db.Querier`. DDL in `schema.sql`; the package does not run migrations. Hot path: `Lookup(ctx, hash) (*KeyRecord, error)` is one `SELECT` keyed by `key_hash`. Mint path: `Insert(ctx, InsertParams{KeyHash, Prefix, Subject, Scopes, Role, Description, ExpiresAt}) (id, error)` returns `*errs.Error{KindAlreadyExists}` on unique-violation.

**Admin / operator API:** `Get(ctx, id) (*KeyInfo, error)` returns the full row projection minus the hash (`KeyInfo{ID, Subject, Scopes, Role, Description, Prefix, CreatedAt, ExpiresAt, RevokedAt, LastUsedAt}`); `ListBySubject(ctx, subject) ([]KeyInfo, error)` returns every record for the subject ordered `created_at DESC` (active + expired + revoked — filtering is caller-side); `RevokeBySubject(ctx, subject) (int, error)` bulk-revokes active rows for incident response (idempotent — unknown subject returns 0); `Stats(ctx) (StoreStats{Active, Expired, Revoked, Total}, error)` reports disjoint buckets in one round trip (revoke wins over expiry); `DeleteExpired(ctx, before time.Time) (int, error)` is the GC sweep (`revoked_at < before` OR `expires_at < before AND revoked_at IS NULL`; active rows untouched).

**Rotation / scope edit:** `Rotate(ctx, id, newHash, newPrefix) error` atomically swaps `key_hash + key_prefix` on an active row preserving id/subject/scopes/role/created_at (`NotFound` on revoked, `KindValidation` on empty hash); `UpdateScopes(ctx, id, scopes) error` replaces scopes without a hash rotation (caller's plain key keeps working; `NotFound` on revoked).

**Schema:** `key_prefix text NOT NULL DEFAULT ''` column stores a short head of the plain key (recommended 6–12 chars, e.g. `"ak_abcd"`) for admin-UI display; `ALTER TABLE … ADD COLUMN IF NOT EXISTS` follows the `CREATE TABLE` so existing deployments migrate cleanly.

Stable error Codes (`apikeypg_insert_failed` / `apikeypg_lookup_failed` / `apikeypg_revoke_failed` / `apikeypg_list_failed` / `apikeypg_stats_failed` / `apikeypg_delete_failed` / `apikeypg_rotate_failed` / `apikeypg_update_failed`; NotFound surfaces as `auth.CodeAPIKeyInvalid`). Tests use `testcontainers-go/modules/postgres` — Docker required.

## `auth/fibermount/`

Wires an `auth.Auth[C]`'s middleware factories into a `*fibermap.Engine[T]` in one call (`fibermount.MountMiddlewareFactories(eng, a)`). The bridge lives in a subpackage so core `auth/` stays free of any `fibermap` import.

## `auth/sessions/`

Server-side cookie sessions for browser-first apps where JWT's "no server-side revocation" hurts. `*Manager[C]` is generic over the kit's `Claims[C]`; constructed via `(*auth.Auth[C]).Sessions(cfg, ...ManagerOption)`.

`Manager.Issue(c, subject, claims, scopes, roles)` mints a session, persists to `SessionStore`, writes the HttpOnly+Secure+SameSite cookie. `Manager.Logout(c)` (cookie-driven) / `Manager.RevokeByID(ctx, id)` (admin-driven, idempotent — empty id short-circuits) / `Manager.LogoutEverywhere(ctx, subject)` (bulk delete, count surfaces in hook payload when store implements `Lister`).

`Manager.Middleware(mode)` reads cookie → loads Session → sliding-refreshes (`newExp = min(now + IdleTimeout, CreatedAt + TTL)`, capped at absolute TTL) → rebuilds `*Principal[C]` in the same Locals slot Bearer uses (so RequireScope / RequireRole / From[C] / Subject[C] all work transparently).

**Observability + hooks** via Options on Sessions: `WithMetrics(reg)` registers `sessions_ops_total{op,outcome}` (op: issue|logout|logout_all|revoke|middleware; outcome: ok|error; middleware also missing|invalid|expired|claims_decode) + `sessions_op_duration_seconds{op}` histogram; `WithLogger(slog)` is silent except for hook panic-recovery; `WithOnIssue(fn)` for Sentry user-scope binding on login; `WithOnLogout(fn)` fires for both cookie Logout and admin RevokeByID (subject empty when no cookie was present); `WithOnLogoutEverywhere(fn)` reports `count` of revoked sessions (-1 when Store does not implement Lister); `WithOnExpire(fn)` fires inside Middleware when an expired session is deleted in-line — distinct from explicit logout so SIEMs can split timeout vs explicit revoke. All hooks panic-safe (recovered + WARN-logged via WithLogger).

`Lister` (optional interface a SessionStore MAY implement) surfaces `ListBySubject(ctx, subject) ([]Session, error)` (newest-first by CreatedAt) and `Stats(ctx) (StoreStats{Active, Expired, Total}, error)` — `MemoryStore` and `sessionsredis.Store` both implement it.

Schema-drift between deploys (changed C, forgot to migrate) → middleware force-logout + cookie clear, never a 500. Sliding cookie attributes: HttpOnly (always), Secure (default true, flip via `InsecureCookie=true` for local-dev HTTP only), `SameSite=Lax` default. Stable Codes: `sessions_missing`, `sessions_invalid_id`, `sessions_store_failed`, `sessions_claims_decode`, `sessions_invalid_config`.

## `auth/sessionsredis/`

Redis-backed `auth/sessions.SessionStore`. Each session is one HASH (`<prefix>session:<id>`) with `EXPIREAT`; subject SET (`<prefix>session:subject:<subject>`) indexes sessions per user so `DeleteForSubject` is O(N) over the user's own sessions, not the whole keyspace.

`New(c *redis.Client, prefix string)` — caller owns the client; the `prefix` namespaces every key so multiple services / tenants can share one Redis.

Implements `sessions.Lister`: `ListBySubject` walks the subject SET + pipelined HGetAll (stale members whose HASH was EXPIREATd are silently skipped); `Stats(ctx)` walks `<prefix>session:*` via SCAN excluding `*session:subject:*` aux sets + pipelined HMGET — O(N), admin/diagnostic only; EXPIREATd records are invisible (Redis dropped them) so `Expired = 0` always on this backend. `Touch` advances `LastSeenAt` + `ExpiresAt` and re-arms EXPIREAT on both the session HASH and the subject SET.

Codes: `sessionsredis_transport` wrapping Redis transport errors as `KindUnavailable`. Tests use `testcontainers-go/modules/redis` — Docker required.