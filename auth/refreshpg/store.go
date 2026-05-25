// Package refreshpg implements auth.RefreshStore on top of Postgres via the
// kit's db.Querier interface. The DDL lives in schema.sql next to this file;
// downstream services run it through their own migration tool.
package refreshpg

import (
	"context"
	"errors"
	"time"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

// Store is the Postgres-backed RefreshStore. Pool ownership stays with the caller.
type Store struct {
	q db.Querier
}

// New takes any db.Querier (so callers can pass a *db.DB or a *db.Tx). The
// most common case — long-lived service — passes *db.DB.
func New(q db.Querier) *Store { return &Store{q: q} }

// Issue inserts a row. pgx errors are funneled through db/'s mapPgxErr (via
// the Querier), so a unique-key collision returns *errs.Error{KindAlreadyExists}.
func (s *Store) Issue(ctx context.Context, r auth.Record) error {
	const q = `
		INSERT INTO auth_refresh_tokens
		    (token_hash, family_id, parent_hash, subject, issued_at, expires_at, user_agent, ip)
		VALUES ($1,$2,$3,$4,$5,$6,$7, NULLIF($8,'')::inet)
	`
	_, err := s.q.Exec(ctx, q,
		r.TokenHash[:], r.FamilyID, r.ParentHash[:], r.Subject,
		r.IssuedAt, r.ExpiresAt, r.UserAgent, r.IP)
	if err != nil {
		// db.Querier funnels pgx errors through mapPgxErr, so a unique-key
		// collision arrives as *errs.Error{KindAlreadyExists}. Pass that through
		// unchanged so callers can switch on Kind/Code. Any other failure is
		// re-kinded as KindUnavailable with auth.CodeStoreUnavailable.
		if e, ok := errors.AsType[*errs.Error](err); ok && e.Kind == errs.KindAlreadyExists {
			return err
		}
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh issue failed")
	}
	return nil
}

// Consume atomically marks a refresh token consumed if it is live, valid, and
// not expired; otherwise it diagnoses the failure mode and (for reuse) revokes
// the whole family before returning the error.
//
// The implementation is two queries: a single UPDATE ... RETURNING that
// succeeds for the live-and-valid case, and a follow-up SELECT only in the
// failure case to disambiguate not_found / expired / reused.
func (s *Store) Consume(ctx context.Context, tokenHash [32]byte, now time.Time) (auth.Record, error) {
	const update = `
		UPDATE auth_refresh_tokens
		SET    consumed_at = $2
		WHERE  token_hash = $1
		  AND  consumed_at IS NULL
		  AND  revoked_at  IS NULL
		  AND  expires_at  > $2
		RETURNING token_hash, family_id, parent_hash, subject, issued_at, expires_at,
		          user_agent, COALESCE(host(ip), '')
	`
	rec, ok, err := scanOne(s.q.QueryRow(ctx, update, tokenHash[:], now))
	if err != nil {
		return auth.Record{}, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh consume failed")
	}
	if ok {
		return rec, nil
	}
	// Diagnose the failure mode.
	const diag = `
		SELECT consumed_at IS NOT NULL OR revoked_at IS NOT NULL AS used,
		       expires_at <= $2                                  AS expired,
		       family_id
		FROM   auth_refresh_tokens
		WHERE  token_hash = $1
	`
	var used, expired bool
	var familyID string
	if err := s.q.QueryRow(ctx, diag, tokenHash[:], now).Scan(&used, &expired, &familyID); err != nil {
		var e *errs.Error
		if errors.As(err, &e) && e.Kind == errs.KindNotFound {
			return auth.Record{}, errs.Unauthorized(auth.CodeRefreshInvalid, "refresh token unknown")
		}
		return auth.Record{}, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh diag failed")
	}
	switch {
	case used:
		if err := s.RevokeFamily(ctx, familyID); err != nil {
			return auth.Record{}, err
		}
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshReused, "refresh token reused")
	case expired:
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshExpired, "refresh token expired")
	default:
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshInvalid, "refresh token unknown")
	}
}

// RevokeFamily marks every live token in the family as revoked. Idempotent
// via `COALESCE(revoked_at, now())` + `WHERE revoked_at IS NULL`.
func (s *Store) RevokeFamily(ctx context.Context, familyID string) error {
	const q = `
		UPDATE auth_refresh_tokens
		SET    revoked_at = COALESCE(revoked_at, now())
		WHERE  family_id  = $1
		  AND  revoked_at IS NULL
	`
	if _, err := s.q.Exec(ctx, q, familyID); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh family revoke failed")
	}
	return nil
}

func scanOne(row interface{ Scan(...any) error }) (auth.Record, bool, error) {
	var r auth.Record
	var ip string
	tokenHash := make([]byte, 0, 32)
	parentHash := make([]byte, 0, 32)
	err := row.Scan(&tokenHash, &r.FamilyID, &parentHash, &r.Subject,
		&r.IssuedAt, &r.ExpiresAt, &r.UserAgent, &ip)
	if err != nil {
		if e, ok := errors.AsType[*errs.Error](err); ok && e.Kind == errs.KindNotFound {
			return auth.Record{}, false, nil
		}
		return auth.Record{}, false, err
	}
	copy(r.TokenHash[:], tokenHash)
	copy(r.ParentHash[:], parentHash)
	r.IP = ip
	return r, true, nil
}

// RevokeSubject revokes every live token belonging to the subject. Idempotent.
func (s *Store) RevokeSubject(ctx context.Context, subject string) error {
	const q = `
		UPDATE auth_refresh_tokens
		SET    revoked_at = COALESCE(revoked_at, now())
		WHERE  subject = $1
		  AND  revoked_at IS NULL
	`
	if _, err := s.q.Exec(ctx, q, subject); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh subject revoke failed")
	}
	return nil
}

// GarbageCollect deletes records with expires_at <= now. Returns the number of
// rows removed.
func (s *Store) GarbageCollect(ctx context.Context, now time.Time) (int64, error) {
	const q = `DELETE FROM auth_refresh_tokens WHERE expires_at <= $1`
	tag, err := s.q.Exec(ctx, q, now)
	if err != nil {
		return 0, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh GC failed")
	}
	return tag.RowsAffected(), nil
}

// Compile-time interface assertion.
var _ auth.RefreshStore = (*Store)(nil)
