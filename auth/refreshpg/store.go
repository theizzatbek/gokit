// Package refreshpg implements auth.RefreshStore on top of Postgres via the
// kit's db.Querier interface. The DDL lives in schema.sql next to this file;
// downstream services run it through their own migration tool.
package refreshpg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/sqb"
	"github.com/theizzatbek/gokit/errs"
)

// Store is the Postgres-backed RefreshStore. Pool ownership stays with
// the caller. Observability + lifecycle hooks are opt-in via [Option].
type Store struct {
	q       db.Querier
	logger  *slog.Logger
	metrics *metrics

	onConsumeReused ConsumeReusedHook
	onFamilyRevoke  FamilyRevokeHook
	onSubjectRevoke SubjectRevokeHook
	onIPRevoke      IPRevokeHook
}

// New takes any db.Querier (so callers can pass a *db.DB or a *db.Tx). The
// most common case — long-lived service — passes *db.DB. Trailing options
// enable metrics / logging / hooks; the zero-option form is unchanged
// from earlier versions.
func New(q db.Querier, opts ...Option) *Store {
	o := storeOpts{}
	for _, fn := range opts {
		fn(&o)
	}
	s := &Store{
		q:               q,
		logger:          o.logger,
		onConsumeReused: o.onConsumeReused,
		onFamilyRevoke:  o.onFamilyRevoke,
		onSubjectRevoke: o.onSubjectRevoke,
		onIPRevoke:      o.onIPRevoke,
	}
	if o.metrics != nil {
		s.metrics = newMetrics(o.metrics)
	}
	return s
}

// recordColumns is the canonical SELECT/RETURNING column order for the
// fully-populated auth.Record shape. host(ip) is a Postgres function call
// rendering inet → text, with COALESCE collapsing NULL to ” so Go-side
// Scan into a plain string works.
var recordColumns = []string{
	"token_hash", "family_id", "parent_hash", "subject",
	"issued_at", "expires_at", "user_agent", "COALESCE(host(ip), '')",
}

var recordReturning = "RETURNING " + strings.Join(recordColumns, ", ")

// Issue inserts a row. pgx errors are funneled through db/'s mapPgxErr (via
// the Querier), so a unique-key collision returns *errs.Error{KindAlreadyExists}.
func (s *Store) Issue(ctx context.Context, r auth.Record) error {
	start := time.Now()
	_, err := sqb.Exec(ctx, s.q, sqb.Builder.
		Insert("auth_refresh_tokens").
		Columns("token_hash", "family_id", "parent_hash", "subject",
			"issued_at", "expires_at", "user_agent", "ip").
		Values(r.TokenHash[:], r.FamilyID, r.ParentHash[:], r.Subject,
			r.IssuedAt, r.ExpiresAt, r.UserAgent,
			sq.Expr("NULLIF(?, '')::inet", r.IP)))
	s.metrics.observe("issue", time.Since(start).Seconds())
	if err != nil {
		// db.Querier funnels pgx errors through mapPgxErr, so a unique-key
		// collision arrives as *errs.Error{KindAlreadyExists}. Pass that through
		// unchanged so callers can switch on Kind/Code. Any other failure is
		// re-kinded as KindUnavailable with auth.CodeStoreUnavailable.
		if e, ok := errors.AsType[*errs.Error](err); ok && e.Kind == errs.KindAlreadyExists {
			s.metrics.inc("issue", "error")
			return err
		}
		s.metrics.inc("issue", "error")
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh issue failed")
	}
	s.metrics.inc("issue", "ok")
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
	start := time.Now()
	defer func() { s.metrics.observe("consume", time.Since(start).Seconds()) }()

	row := sqb.QueryRow(ctx, s.q, sqb.Builder.
		Update("auth_refresh_tokens").
		Set("consumed_at", now).
		Where(sq.Eq{
			"token_hash":  tokenHash[:],
			"consumed_at": nil,
			"revoked_at":  nil,
		}).
		Where(sq.Gt{"expires_at": now}).
		Suffix(recordReturning))

	rec, ok, err := scanOne(row)
	if err != nil {
		s.metrics.inc("consume", "error")
		return auth.Record{}, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh consume failed")
	}
	if ok {
		s.metrics.inc("consume", "ok")
		return rec, nil
	}

	// Diagnose the failure mode. `expires_at <= ? AS expired` carries a
	// placeholder, so it is added via .Column(expr, arg) — squirrel tracks
	// the arg alongside the column and renumbers $N correctly.
	var used, expired bool
	var familyID, subject string
	diag := sqb.Builder.
		Select().
		Column("consumed_at IS NOT NULL OR revoked_at IS NOT NULL AS used").
		Column("expires_at <= ? AS expired", now).
		Column("family_id").
		Column("subject").
		From("auth_refresh_tokens").
		Where(sq.Eq{"token_hash": tokenHash[:]})
	if err := sqb.QueryRow(ctx, s.q, diag).Scan(&used, &expired, &familyID, &subject); err != nil {
		var e *errs.Error
		if errors.As(err, &e) && e.Kind == errs.KindNotFound {
			s.metrics.inc("consume", "missing")
			return auth.Record{}, errs.Unauthorized(auth.CodeRefreshInvalid, "refresh token unknown")
		}
		s.metrics.inc("consume", "error")
		return auth.Record{}, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh diag failed")
	}
	switch {
	case used:
		if err := s.RevokeFamily(ctx, familyID); err != nil {
			s.metrics.inc("consume", "error")
			return auth.Record{}, err
		}
		s.metrics.inc("consume", "reused")
		s.fireConsumeReused(ctx, familyID, subject)
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshReused, "refresh token reused")
	case expired:
		s.metrics.inc("consume", "expired")
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshExpired, "refresh token expired")
	default:
		s.metrics.inc("consume", "missing")
		return auth.Record{}, errs.Unauthorized(auth.CodeRefreshInvalid, "refresh token unknown")
	}
}

// RevokeFamily marks every live token in the family as revoked. Idempotent
// via `COALESCE(revoked_at, now())` + `WHERE revoked_at IS NULL`.
func (s *Store) RevokeFamily(ctx context.Context, familyID string) error {
	start := time.Now()
	tag, err := sqb.Exec(ctx, s.q, sqb.Builder.
		Update("auth_refresh_tokens").
		Set("revoked_at", sq.Expr("COALESCE(revoked_at, now())")).
		Where(sq.Eq{"family_id": familyID, "revoked_at": nil}))
	s.metrics.observe("revoke_family", time.Since(start).Seconds())
	if err != nil {
		s.metrics.inc("revoke_family", "error")
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh family revoke failed")
	}
	s.metrics.inc("revoke_family", "ok")
	s.fireFamilyRevoke(ctx, familyID, tag.RowsAffected())
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
	start := time.Now()
	tag, err := sqb.Exec(ctx, s.q, sqb.Builder.
		Update("auth_refresh_tokens").
		Set("revoked_at", sq.Expr("COALESCE(revoked_at, now())")).
		Where(sq.Eq{"subject": subject, "revoked_at": nil}))
	s.metrics.observe("revoke_subject", time.Since(start).Seconds())
	if err != nil {
		s.metrics.inc("revoke_subject", "error")
		return errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh subject revoke failed")
	}
	s.metrics.inc("revoke_subject", "ok")
	s.fireSubjectRevoke(ctx, subject, tag.RowsAffected())
	return nil
}

// GarbageCollect deletes records with expires_at <= now. Returns the number of
// rows removed.
func (s *Store) GarbageCollect(ctx context.Context, now time.Time) (int64, error) {
	start := time.Now()
	tag, err := sqb.Exec(ctx, s.q, sqb.Builder.
		Delete("auth_refresh_tokens").
		Where(sq.LtOrEq{"expires_at": now}))
	s.metrics.observe("gc", time.Since(start).Seconds())
	if err != nil {
		s.metrics.inc("gc", "error")
		return 0, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh GC failed")
	}
	s.metrics.inc("gc", "ok")
	return tag.RowsAffected(), nil
}

// GarbageCollectBatch is the chunked variant of [Store.GarbageCollect] for
// very large tables: it loops `DELETE … WHERE expires_at <= now LIMIT N`
// until the affected count is zero, returning the total number of rows
// removed. Use when [Store.GarbageCollect] would lock the table for too
// long under nightly cron pressure.
//
// `limit ≤ 0` is treated as 1000. `maxIterations ≤ 0` defaults to 1024
// (a defence against pathological GC loops). On ctx cancel the method
// returns whatever it has already deleted plus ctx.Err() — partial
// progress is the safe behaviour for a sweeper.
func (s *Store) GarbageCollectBatch(ctx context.Context, now time.Time, limit, maxIterations int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}
	if maxIterations <= 0 {
		maxIterations = 1024
	}
	start := time.Now()
	defer func() { s.metrics.observe("gc", time.Since(start).Seconds()) }()

	// Postgres does not support DELETE … LIMIT directly; wrap via a
	// scalar subquery on the primary key. token_hash is the PK so the
	// inner SELECT is index-only.
	const sql = `
		DELETE FROM auth_refresh_tokens
		WHERE token_hash IN (
		    SELECT token_hash
		    FROM auth_refresh_tokens
		    WHERE expires_at <= $1
		    LIMIT $2
		)
	`
	var total int64
	for i := 0; i < maxIterations; i++ {
		if err := ctx.Err(); err != nil {
			s.metrics.inc("gc", "error")
			return total, err
		}
		tag, err := s.q.Exec(ctx, sql, now, limit)
		if err != nil {
			s.metrics.inc("gc", "error")
			return total, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh GC batch failed")
		}
		n := tag.RowsAffected()
		total += n
		if n == 0 {
			break
		}
	}
	s.metrics.inc("gc", "ok")
	return total, nil
}

// SessionInfo is the admin-side projection of an issued refresh-token row.
// Returned by [Store.ListBySubject]. NEVER includes `token_hash` — the
// hash is secret material and stays in the store.
type SessionInfo struct {
	FamilyID   string
	Subject    string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	ConsumedAt time.Time // zero = still live
	RevokedAt  time.Time // zero = active
	UserAgent  string
	IP         string
	State      string // "active" | "consumed" | "revoked" | "expired"
}

// ListBySubject returns every refresh-token row for the subject ordered
// by `issued_at DESC`. Surface this to admin UIs that render an "active
// sessions" list — UI filters by `State` field to hide history.
//
// Empty subject returns an empty slice without touching the DB.
func (s *Store) ListBySubject(ctx context.Context, subject string) ([]SessionInfo, error) {
	if subject == "" {
		return []SessionInfo{}, nil
	}
	start := time.Now()
	defer func() { s.metrics.observe("list", time.Since(start).Seconds()) }()

	const sql = `
		SELECT family_id::text, subject,
		       issued_at, expires_at,
		       COALESCE(consumed_at, 'epoch'::timestamptz),
		       COALESCE(revoked_at,  'epoch'::timestamptz),
		       user_agent, COALESCE(host(ip), '')
		FROM auth_refresh_tokens
		WHERE subject = $1
		ORDER BY issued_at DESC
	`
	rows, err := s.q.Query(ctx, sql, subject)
	if err != nil {
		s.metrics.inc("list", "error")
		var e *errs.Error
		if errors.As(err, &e) {
			return nil, err
		}
		return nil, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh list failed")
	}
	defer rows.Close()
	out := make([]SessionInfo, 0, 4)
	epoch := time.Unix(0, 0).UTC()
	now := time.Now()
	for rows.Next() {
		var info SessionInfo
		var consumedAt, revokedAt time.Time
		if err := rows.Scan(&info.FamilyID, &info.Subject,
			&info.IssuedAt, &info.ExpiresAt,
			&consumedAt, &revokedAt,
			&info.UserAgent, &info.IP,
		); err != nil {
			s.metrics.inc("list", "error")
			return nil, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh list scan failed")
		}
		if !consumedAt.Equal(epoch) {
			info.ConsumedAt = consumedAt
		}
		if !revokedAt.Equal(epoch) {
			info.RevokedAt = revokedAt
		}
		info.State = sessionState(info, now)
		out = append(out, info)
	}
	if err := rows.Err(); err != nil {
		s.metrics.inc("list", "error")
		return nil, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh list rows failed")
	}
	s.metrics.inc("list", "ok")
	return out, nil
}

// sessionState classifies a SessionInfo into one of the four states
// surfaced to admin UIs. Revoke wins over expiry; consume wins over
// active. Buckets are disjoint.
func sessionState(info SessionInfo, now time.Time) string {
	switch {
	case !info.RevokedAt.IsZero():
		return "revoked"
	case !info.ConsumedAt.IsZero():
		return "consumed"
	case info.ExpiresAt.Before(now) || info.ExpiresAt.Equal(now):
		return "expired"
	default:
		return "active"
	}
}

// StoreStats is the rollup returned by [Store.Stats]. Buckets are
// disjoint:
//
//	Active   = consumed_at IS NULL AND revoked_at IS NULL AND expires_at > NOW()
//	Consumed = consumed_at IS NOT NULL AND revoked_at IS NULL
//	Revoked  = revoked_at IS NOT NULL (revoke wins over consume/expiry)
//	Expired  = consumed_at IS NULL AND revoked_at IS NULL AND expires_at <= NOW()
//
// Total = Active + Consumed + Revoked + Expired.
type StoreStats struct {
	Active   int
	Consumed int
	Revoked  int
	Expired  int
	Total    int
}

// Stats returns the disjoint-bucket rollup in a single round trip.
// Suitable for /admin or /metrics-pull endpoints; not on a hot path.
func (s *Store) Stats(ctx context.Context) (StoreStats, error) {
	start := time.Now()
	defer func() { s.metrics.observe("stats", time.Since(start).Seconds()) }()

	const sql = `
		SELECT
		    COUNT(*) FILTER (
		        WHERE consumed_at IS NULL AND revoked_at IS NULL AND expires_at > NOW()
		    ) AS active,
		    COUNT(*) FILTER (
		        WHERE consumed_at IS NOT NULL AND revoked_at IS NULL
		    ) AS consumed,
		    COUNT(*) FILTER (WHERE revoked_at IS NOT NULL) AS revoked,
		    COUNT(*) FILTER (
		        WHERE consumed_at IS NULL AND revoked_at IS NULL AND expires_at <= NOW()
		    ) AS expired,
		    COUNT(*) AS total
		FROM auth_refresh_tokens
	`
	var out StoreStats
	row := s.q.QueryRow(ctx, sql)
	if err := row.Scan(&out.Active, &out.Consumed, &out.Revoked, &out.Expired, &out.Total); err != nil {
		s.metrics.inc("stats", "error")
		var e *errs.Error
		if errors.As(err, &e) {
			return StoreStats{}, err
		}
		return StoreStats{}, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh stats failed")
	}
	s.metrics.inc("stats", "ok")
	return out, nil
}

// RevokeByIP revokes every currently-active refresh token issued from
// the supplied IP address. Use for incident response — a leaked
// session cookie or a compromised egress IP — to evict every active
// family bound to that address in one round trip.
//
// Returns the number of rows revoked. Zero is NOT an error
// (idempotent against repeat calls and against IPs that never issued).
// Empty `ip` returns 0 without touching the DB.
//
// The supplied string is parsed as an `inet` literal by Postgres —
// callers can pass either a v4 address ("203.0.113.7") or a v6
// ("2001:db8::1"). Mask/CIDR forms are not supported (single-host
// semantics are what incident response actually wants).
func (s *Store) RevokeByIP(ctx context.Context, ip string) (int64, error) {
	if ip == "" {
		return 0, nil
	}
	start := time.Now()
	tag, err := sqb.Exec(ctx, s.q, sqb.Builder.
		Update("auth_refresh_tokens").
		Set("revoked_at", sq.Expr("COALESCE(revoked_at, now())")).
		Where(sq.Expr("ip = ?::inet", ip)).
		Where(sq.Eq{"revoked_at": nil}))
	s.metrics.observe("revoke_ip", time.Since(start).Seconds())
	if err != nil {
		s.metrics.inc("revoke_ip", "error")
		return 0, errs.Wrap(err, errs.KindUnavailable, auth.CodeStoreUnavailable, "refresh ip revoke failed")
	}
	s.metrics.inc("revoke_ip", "ok")
	n := tag.RowsAffected()
	s.fireIPRevoke(ctx, ip, n)
	return n, nil
}

// fireConsumeReused invokes the OnConsumeReused hook under a recover.
func (s *Store) fireConsumeReused(ctx context.Context, familyID, subject string) {
	if s.onConsumeReused == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && s.logger != nil {
			s.logger.WarnContext(ctx, "refreshpg: OnConsumeReused panic recovered",
				"panic", fmt.Sprint(r), "family_id", familyID)
		}
	}()
	s.onConsumeReused(ctx, familyID, subject)
}

// fireFamilyRevoke invokes the OnFamilyRevoke hook under a recover.
func (s *Store) fireFamilyRevoke(ctx context.Context, familyID string, count int64) {
	if s.onFamilyRevoke == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && s.logger != nil {
			s.logger.WarnContext(ctx, "refreshpg: OnFamilyRevoke panic recovered",
				"panic", fmt.Sprint(r), "family_id", familyID)
		}
	}()
	s.onFamilyRevoke(ctx, familyID, count)
}

// fireSubjectRevoke invokes the OnSubjectRevoke hook under a recover.
func (s *Store) fireSubjectRevoke(ctx context.Context, subject string, count int64) {
	if s.onSubjectRevoke == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && s.logger != nil {
			s.logger.WarnContext(ctx, "refreshpg: OnSubjectRevoke panic recovered",
				"panic", fmt.Sprint(r), "subject", subject)
		}
	}()
	s.onSubjectRevoke(ctx, subject, count)
}

// fireIPRevoke invokes the OnIPRevoke hook under a recover.
func (s *Store) fireIPRevoke(ctx context.Context, ip string, count int64) {
	if s.onIPRevoke == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && s.logger != nil {
			s.logger.WarnContext(ctx, "refreshpg: OnIPRevoke panic recovered",
				"panic", fmt.Sprint(r), "ip", ip)
		}
	}()
	s.onIPRevoke(ctx, ip, count)
}

// Compile-time interface assertion.
var _ auth.RefreshStore = (*Store)(nil)
