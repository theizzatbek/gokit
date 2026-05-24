// Package refreshpg implements auth.RefreshStore on top of Postgres via the
// kit's db.Querier interface. The DDL lives in schema.sql next to this file;
// downstream services run it through their own migration tool.
package refreshpg

import (
	"context"
	"errors"

	"github.com/theizzatbek/fibermap/auth"
	"github.com/theizzatbek/fibermap/db"
	"github.com/theizzatbek/fibermap/errs"
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

// Consume / RevokeFamily / RevokeSubject / GarbageCollect are added in Tasks 20+21.
