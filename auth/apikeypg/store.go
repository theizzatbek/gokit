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
)

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
		INSERT INTO auth_api_keys (key_hash, subject, scopes, role, description, expires_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, 'epoch'::timestamptz))
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
	row := s.q.QueryRow(ctx, sql, p.KeyHash, p.Subject, scopes, p.Role, p.Description, expires)
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
