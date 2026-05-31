# auth/apikeypg

Postgres-backed [`auth.KeyStore`](../README.md#api-key-authentication) for the kit's API-key middleware. Thin wrapper over `db.Querier` — pool ownership stays with the caller.

## Quickstart

```go
// 1. Apply schema (or fold into your migration runner).
_, _ = svc.DB.Exec(ctx, apikeypg.Schema())

// 2. Construct the store.
store := apikeypg.New(svc.DB)

// 3. Plug into auth.APIKey + fibermount.
app.Use(authObj.APIKey(store))
fibermount.MountAPIKeyFactory(svc.Engine, authObj, store)

// Admin path: mint a fresh key.
plain := "ak_" + randomToken()
hash  := auth.HashAPIKey(plain, cfg.APIKeyHashSecret)
id, _ := store.Insert(ctx, apikeypg.InsertParams{
    KeyHash: hash, Subject: "svc-orders",
    Scopes:  []string{"orders:read"}, Role: "service",
    ExpiresAt: time.Now().Add(90*24*time.Hour),
    Description: "issued by admin@example.com on 2026-05-31",
})
// Hand `plain` back to the caller ONCE — only the hash is stored.
```

## API surface

| Method | Returns | Notes |
|---|---|---|
| `New(q db.Querier) *Store` | — | Construct over any Querier (typically `*db.DB`). |
| `Schema() string` | embedded DDL | Run via migration tool or `db.Exec` at boot. |
| `Lookup(ctx, hash) (*KeyRecord, error)` | record or NotFound | Hot path; single `SELECT`. |
| `Insert(ctx, InsertParams) (id, error)` | new row id | Returns `*errs.Error{KindAlreadyExists}` on key-hash collision. |
| `RevokeByID(ctx, id) error` | nil on success | Sets `revoked_at = NOW()`. Idempotent against re-revokes (returns `NotFound`). |

## Schema

`auth_api_keys` columns:

| Column | Type | Notes |
|---|---|---|
| `id` | `uuid PRIMARY KEY DEFAULT gen_random_uuid()` | Public identifier. Surfaces in `Principal.JTI`. |
| `key_hash` | `bytea NOT NULL UNIQUE` | HMAC-SHA256 hash; the lookup index. |
| `subject` | `text NOT NULL` | Principal subject (service / user id). |
| `scopes` | `text[] NOT NULL DEFAULT '{}'` | Auth scopes the key carries. |
| `role` | `text NOT NULL DEFAULT ''` | Optional broad role. |
| `description` | `text NOT NULL DEFAULT ''` | Free-text for admin/audit. |
| `created_at` | `timestamptz NOT NULL DEFAULT NOW()` | Mint time. |
| `expires_at` | `timestamptz` | NULL = no expiry. |
| `revoked_at` | `timestamptz` | NULL = active. |
| `last_used_at` | `timestamptz` | Optional — kit doesn't bump on every Lookup (would turn the read into a write). Wire an async writer if needed. |

Two indexes:

- `auth_api_keys_subject_idx (subject)` — admin lookups / revoke-all-for-subject.
- `auth_api_keys_expires_at_idx (expires_at) WHERE expires_at IS NOT NULL AND revoked_at IS NULL` — partial index for nightly expiry-cleanup cron.

## Error codes

| Code | Where | Meaning |
|---|---|---|
| `api_key_invalid` | `Lookup`, `RevokeByID` | No matching row (NotFound). The auth middleware maps this to 401. |
| `apikeypg_insert_failed` | `Insert` | Non-conflict INSERT failure. |
| `apikeypg_lookup_failed` | `Lookup` | Non-NotFound SELECT failure (network / server down). |
| `apikeypg_revoke_failed` | `RevokeByID` | UPDATE failed for non-NotFound reason. |

## Testing

Tests use `testcontainers-go/modules/postgres` (Docker required). Skips under `-short`.

```bash
go test ./auth/apikeypg/...
```

## See also

- [`auth`](../README.md) — the parent package; `auth.APIKey` middleware + `auth.KeyStore` interface
- [`db`](../../db/README.md) — `db.Querier` is what `apikeypg.Store` consumes
- [`auth/refreshpg`](../refreshpg/README.md) — sibling Postgres adapter for the refresh-token side
