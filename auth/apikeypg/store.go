// Package apikeypg implements auth.KeyStore on top of Postgres via
// the kit's db.Querier interface. The DDL lives in schema.sql next
// to this file; downstream services run it through their own
// migration tool (or via embed.FS + db.Exec at boot — see urlshort's
// applyMigrations helper for the smallest reasonable bootstrap).
package apikeypg

import (
	"context"
	_ "embed"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

//go:embed schema.sql
var schemaSQL string

// Schema returns the embedded DDL for the auth_api_keys table +
// indexes. Caller applies via migration runner or `db.Exec`. Every
// statement uses IF NOT EXISTS — safe to run on startup.
func Schema() string { return schemaSQL }

// Stable error Codes for caller-side switching.
const (
	// CodeKeyInsertFailed — Insert's UPSERT failed for non-conflict
	// reasons (unique violation surfaces as KindAlreadyExists via
	// db's mapPgxErr).
	CodeKeyInsertFailed = "apikeypg_insert_failed"

	// CodeKeyLookupFailed — Lookup's SELECT failed for non-NotFound
	// reasons (network, server down).
	CodeKeyLookupFailed = "apikeypg_lookup_failed"

	// CodeKeyRevokeFailed — RevokeByID's UPDATE failed.
	CodeKeyRevokeFailed = "apikeypg_revoke_failed"

	// CodeKeyListFailed — ListBySubject's SELECT failed.
	CodeKeyListFailed = "apikeypg_list_failed"

	// CodeKeyStatsFailed — Stats's SELECT failed.
	CodeKeyStatsFailed = "apikeypg_stats_failed"

	// CodeKeyDeleteFailed — DeleteExpired's DELETE failed.
	CodeKeyDeleteFailed = "apikeypg_delete_failed"

	// CodeKeyRotateFailed — Rotate's UPDATE failed.
	CodeKeyRotateFailed = "apikeypg_rotate_failed"

	// CodeKeyUpdateFailed — UpdateScopes's UPDATE failed.
	CodeKeyUpdateFailed = "apikeypg_update_failed"
)

// KeyInfo is the admin-side projection of a stored API-key record.
// Surfaces every column except key_hash — the hash is the auth-side
// secret material and never leaves the store.
type KeyInfo struct {
	ID          string
	Subject     string
	Scopes      []string
	Role        string
	Description string
	Prefix      string
	CreatedAt   time.Time
	ExpiresAt   time.Time // zero = no expiry
	RevokedAt   time.Time // zero = active
	LastUsedAt  time.Time // zero = never observed
}

// StoreStats is the rollup returned by [Store.Stats]. Total = Active +
// Expired + Revoked.
//
//	Active = revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())
//	Expired = revoked_at IS NULL AND expires_at <= NOW()
//	Revoked = revoked_at IS NOT NULL (regardless of expires_at)
//
// Revoked-AND-Expired collapses to Revoked (revoke wins) so the three
// buckets are disjoint.
type StoreStats struct {
	Active  int
	Expired int
	Revoked int
	Total   int
}

// Store is the Postgres-backed KeyStore. Pool ownership stays with
// the caller — typical usage passes `svc.DB`.
type Store struct {
	q db.Querier
}

// New takes any db.Querier (so callers can pass a *db.DB or a *db.Tx
// for testing). The most common case — long-lived service — passes
// *db.DB.
func New(q db.Querier) *Store { return &Store{q: q} }

// Lookup implements [auth.KeyStore.Lookup]. Returns
// *errs.Error{Kind: NotFound} on pgx.ErrNoRows (the auth middleware
// maps NotFound → 401 with [auth.CodeAPIKeyInvalid]).
func (s *Store) Lookup(ctx context.Context, keyHash []byte) (*auth.KeyRecord, error) {
	const sql = `
		SELECT id::text, subject, scopes, role,
		       COALESCE(expires_at, 'epoch'::timestamptz),
		       COALESCE(revoked_at, 'epoch'::timestamptz)
		FROM auth_api_keys
		WHERE key_hash = $1
	`
	var (
		rec       auth.KeyRecord
		expiresAt time.Time
		revokedAt time.Time
	)
	row := s.q.QueryRow(ctx, sql, keyHash)
	if err := row.Scan(&rec.ID, &rec.Subject, &rec.Scopes, &rec.Role, &expiresAt, &revokedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errs.NotFound(auth.CodeAPIKeyInvalid, "apikeypg: key not found")
		}
		// db.QueryRow already maps pgx errors through mapPgxErr when
		// reached via the *db.DB wrapper, but a raw *pgxpool.Pool /
		// *pgx.Tx surface bypasses that. Wrap the residual error so
		// the caller sees a consistent *errs.Error shape regardless
		// of which Querier was supplied.
		var e *errs.Error
		if errors.As(err, &e) {
			return nil, err
		}
		return nil, errs.Wrap(err, errs.KindInternal, CodeKeyLookupFailed,
			"apikeypg: lookup")
	}
	// epoch sentinel → restore zero time so KeyRecord matches the
	// auth.KeyStore contract (zero = "no expiry / not revoked").
	epoch := time.Unix(0, 0).UTC()
	if !expiresAt.Equal(epoch) {
		rec.ExpiresAt = expiresAt
	}
	if !revokedAt.Equal(epoch) {
		rec.RevokedAt = revokedAt
	}
	return &rec, nil
}

// InsertParams is the subset of fields callers populate on Insert.
// id / created_at / last_used_at are managed by the table.
type InsertParams struct {
	KeyHash     []byte
	Prefix      string // short head of the plain key for admin UI display; empty = column stays empty
	Subject     string
	Scopes      []string
	Role        string
	Description string
	ExpiresAt   time.Time // zero = no expiry
}

// Insert persists a new API key. KeyHash is the HMAC-SHA256 from
// [auth.HashAPIKey] — callers compute it once at mint time, surface
// the plain key to the requesting user, and keep only the hash on
// the server side.
//
// Returns *errs.Error{Kind: AlreadyExists} on unique-violation —
// callers retry with a freshly-generated plain key.
func (s *Store) Insert(ctx context.Context, p InsertParams) (string, error) {
	const sql = `
		INSERT INTO auth_api_keys (key_hash, key_prefix, subject, scopes, role, description, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, 'epoch'::timestamptz))
		RETURNING id::text
	`
	expires := time.Unix(0, 0).UTC()
	if !p.ExpiresAt.IsZero() {
		expires = p.ExpiresAt
	}
	// Postgres NOT NULL + DEFAULT '{}' only fires when scopes is OMITTED,
	// not when explicitly NULL — coerce nil to empty slice so callers
	// don't have to remember.
	scopes := p.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	var id string
	row := s.q.QueryRow(ctx, sql, p.KeyHash, p.Prefix, p.Subject, scopes, p.Role, p.Description, expires)
	if err := row.Scan(&id); err != nil {
		var e *errs.Error
		if errors.As(err, &e) {
			return "", err
		}
		return "", errs.Wrap(err, errs.KindInternal, CodeKeyInsertFailed,
			"apikeypg: insert")
	}
	return id, nil
}

// RevokeByID flips the revoked_at column to NOW() for the supplied
// record id. Subsequent Lookups still return the row (the auth
// middleware checks RevokedAt itself, returning 401 with
// [auth.CodeAPIKeyRevoked]), preserving audit-trail visibility.
//
// Returns *errs.Error{Kind: NotFound} when no row matches.
func (s *Store) RevokeByID(ctx context.Context, id string) error {
	const sql = `
		UPDATE auth_api_keys
		SET revoked_at = NOW()
		WHERE id::text = $1 AND revoked_at IS NULL
	`
	tag, err := s.q.Exec(ctx, sql, id)
	if err != nil {
		var e *errs.Error
		if errors.As(err, &e) {
			return err
		}
		return errs.Wrap(err, errs.KindInternal, CodeKeyRevokeFailed,
			"apikeypg: revoke")
	}
	if tag.RowsAffected() == 0 {
		return errs.NotFound(auth.CodeAPIKeyInvalid,
			"apikeypg: no active key with that id")
	}
	return nil
}

// RevokeBySubject flips revoked_at to NOW() for every currently-active
// key bound to the supplied subject. Use for incident response
// ("revoke every key for svc-orders") or for offboarding flows.
//
// Returns the number of rows revoked; zero is NOT an error (idempotent
// against repeat calls and against subjects that never had a key).
func (s *Store) RevokeBySubject(ctx context.Context, subject string) (int, error) {
	const sql = `
		UPDATE auth_api_keys
		SET revoked_at = NOW()
		WHERE subject = $1 AND revoked_at IS NULL
	`
	tag, err := s.q.Exec(ctx, sql, subject)
	if err != nil {
		var e *errs.Error
		if errors.As(err, &e) {
			return 0, err
		}
		return 0, errs.Wrap(err, errs.KindInternal, CodeKeyRevokeFailed,
			"apikeypg: revoke by subject")
	}
	return int(tag.RowsAffected()), nil
}

// Get returns the admin-side projection of one record by id.
//
// Returns *errs.Error{Kind: NotFound} on miss. The auth-middleware
// hot path uses [Store.Lookup] (by hash) — Get exists for admin lists
// / detail panes / audit views.
func (s *Store) Get(ctx context.Context, id string) (*KeyInfo, error) {
	const sql = `
		SELECT id::text, subject, scopes, role, description, key_prefix,
		       created_at,
		       COALESCE(expires_at,   'epoch'::timestamptz),
		       COALESCE(revoked_at,   'epoch'::timestamptz),
		       COALESCE(last_used_at, 'epoch'::timestamptz)
		FROM auth_api_keys
		WHERE id::text = $1
	`
	row := s.q.QueryRow(ctx, sql, id)
	info, err := scanKeyInfo(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errs.NotFound(auth.CodeAPIKeyInvalid,
				"apikeypg: no key with that id")
		}
		var e *errs.Error
		if errors.As(err, &e) {
			return nil, err
		}
		return nil, errs.Wrap(err, errs.KindInternal, CodeKeyLookupFailed,
			"apikeypg: get")
	}
	return info, nil
}

// ListBySubject returns every record bound to the supplied subject
// (active, expired, revoked) ordered by created_at DESC. Admin UIs
// render this directly; expiry/revocation filtering is the caller's
// job (the kit favours surfacing the full history over hiding state).
//
// Empty subject returns an empty slice without touching the DB.
func (s *Store) ListBySubject(ctx context.Context, subject string) ([]KeyInfo, error) {
	if subject == "" {
		return []KeyInfo{}, nil
	}
	const sql = `
		SELECT id::text, subject, scopes, role, description, key_prefix,
		       created_at,
		       COALESCE(expires_at,   'epoch'::timestamptz),
		       COALESCE(revoked_at,   'epoch'::timestamptz),
		       COALESCE(last_used_at, 'epoch'::timestamptz)
		FROM auth_api_keys
		WHERE subject = $1
		ORDER BY created_at DESC
	`
	rows, err := s.q.Query(ctx, sql, subject)
	if err != nil {
		var e *errs.Error
		if errors.As(err, &e) {
			return nil, err
		}
		return nil, errs.Wrap(err, errs.KindInternal, CodeKeyListFailed,
			"apikeypg: list by subject")
	}
	defer rows.Close()
	out := make([]KeyInfo, 0, 4)
	for rows.Next() {
		info, err := scanKeyInfo(rows)
		if err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, CodeKeyListFailed,
				"apikeypg: list scan")
		}
		out = append(out, *info)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, CodeKeyListFailed,
			"apikeypg: list rows")
	}
	return out, nil
}

// Stats returns active/expired/revoked counts in one round trip.
// Buckets are disjoint — see [StoreStats] for the partition rule.
func (s *Store) Stats(ctx context.Context) (StoreStats, error) {
	const sql = `
		SELECT
		    COUNT(*) FILTER (
		        WHERE revoked_at IS NULL
		          AND (expires_at IS NULL OR expires_at > NOW())
		    ) AS active,
		    COUNT(*) FILTER (
		        WHERE revoked_at IS NULL
		          AND expires_at IS NOT NULL
		          AND expires_at <= NOW()
		    ) AS expired,
		    COUNT(*) FILTER (WHERE revoked_at IS NOT NULL) AS revoked,
		    COUNT(*) AS total
		FROM auth_api_keys
	`
	var s2 StoreStats
	row := s.q.QueryRow(ctx, sql)
	if err := row.Scan(&s2.Active, &s2.Expired, &s2.Revoked, &s2.Total); err != nil {
		var e *errs.Error
		if errors.As(err, &e) {
			return StoreStats{}, err
		}
		return StoreStats{}, errs.Wrap(err, errs.KindInternal, CodeKeyStatsFailed,
			"apikeypg: stats")
	}
	return s2, nil
}

// DeleteExpired permanently removes records whose final-state
// timestamp predates `before`. A row is eligible iff:
//
//   - revoked_at IS NOT NULL AND revoked_at < before, OR
//   - revoked_at IS NULL AND expires_at IS NOT NULL AND expires_at < before
//
// Active rows are never touched. Returns the number of rows deleted.
// Typical schedule: nightly cron with `before = NOW() - 90 days` so
// long-tail audit visibility is preserved.
func (s *Store) DeleteExpired(ctx context.Context, before time.Time) (int, error) {
	const sql = `
		DELETE FROM auth_api_keys
		WHERE (revoked_at IS NOT NULL AND revoked_at < $1)
		   OR (revoked_at IS NULL AND expires_at IS NOT NULL AND expires_at < $1)
	`
	tag, err := s.q.Exec(ctx, sql, before)
	if err != nil {
		var e *errs.Error
		if errors.As(err, &e) {
			return 0, err
		}
		return 0, errs.Wrap(err, errs.KindInternal, CodeKeyDeleteFailed,
			"apikeypg: delete expired")
	}
	return int(tag.RowsAffected()), nil
}

// Rotate atomically swaps the key_hash + key_prefix on an active
// record, preserving id / subject / scopes / role / created_at. Use
// for proactive rotation (a service rotates its own secret, holds the
// same id for audit-trail continuity).
//
// newPrefix may be empty when the caller has no display prefix to
// surface. Returns *errs.Error{Kind: NotFound} when no active key
// matches the id (already revoked or never existed).
func (s *Store) Rotate(ctx context.Context, id string, newHash []byte, newPrefix string) error {
	if len(newHash) == 0 {
		return errs.Validation(CodeKeyRotateFailed,
			"apikeypg: Rotate requires non-empty newHash")
	}
	const sql = `
		UPDATE auth_api_keys
		SET key_hash = $2, key_prefix = $3
		WHERE id::text = $1 AND revoked_at IS NULL
	`
	tag, err := s.q.Exec(ctx, sql, id, newHash, newPrefix)
	if err != nil {
		var e *errs.Error
		if errors.As(err, &e) {
			return err
		}
		return errs.Wrap(err, errs.KindInternal, CodeKeyRotateFailed,
			"apikeypg: rotate")
	}
	if tag.RowsAffected() == 0 {
		return errs.NotFound(auth.CodeAPIKeyInvalid,
			"apikeypg: no active key with that id")
	}
	return nil
}

// UpdateScopes replaces the scopes slice on an active record. Use for
// permission widening / narrowing without forcing a rotation; the
// stored key_hash + plain key the caller holds keep working.
//
// Returns *errs.Error{Kind: NotFound} when no active key matches.
// nil scopes coerces to '{}' to match the column NOT NULL contract.
func (s *Store) UpdateScopes(ctx context.Context, id string, scopes []string) error {
	if scopes == nil {
		scopes = []string{}
	}
	const sql = `
		UPDATE auth_api_keys
		SET scopes = $2
		WHERE id::text = $1 AND revoked_at IS NULL
	`
	tag, err := s.q.Exec(ctx, sql, id, scopes)
	if err != nil {
		var e *errs.Error
		if errors.As(err, &e) {
			return err
		}
		return errs.Wrap(err, errs.KindInternal, CodeKeyUpdateFailed,
			"apikeypg: update scopes")
	}
	if tag.RowsAffected() == 0 {
		return errs.NotFound(auth.CodeAPIKeyInvalid,
			"apikeypg: no active key with that id")
	}
	return nil
}

// scanKeyInfo is the shared row → KeyInfo decoder used by Get and
// ListBySubject. Accepts pgx.Row (single-row Scan path) and pgx.Rows
// (loop scan path) via the lowest-common-denominator interface.
func scanKeyInfo(row interface {
	Scan(dest ...any) error
}) (*KeyInfo, error) {
	var (
		info       KeyInfo
		expiresAt  time.Time
		revokedAt  time.Time
		lastUsedAt time.Time
	)
	if err := row.Scan(
		&info.ID, &info.Subject, &info.Scopes, &info.Role, &info.Description, &info.Prefix,
		&info.CreatedAt, &expiresAt, &revokedAt, &lastUsedAt,
	); err != nil {
		return nil, err
	}
	epoch := time.Unix(0, 0).UTC()
	if !expiresAt.Equal(epoch) {
		info.ExpiresAt = expiresAt
	}
	if !revokedAt.Equal(epoch) {
		info.RevokedAt = revokedAt
	}
	if !lastUsedAt.Equal(epoch) {
		info.LastUsedAt = lastUsedAt
	}
	return &info, nil
}
