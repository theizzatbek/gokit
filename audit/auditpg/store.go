package auditpg

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/theizzatbek/gokit/audit"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/lock"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Stable error Code constants returned by this package.
const (
	// CodeTransport — DB call returned an error.
	CodeTransport = "auditpg_transport"

	// CodeNilDB — Store built with nil *db.DB.
	CodeNilDB = "auditpg_nil_db"
)

//go:embed schema.sql
var schemaDDL string

// Schema returns the embedded DDL. Idempotent — safe to apply
// repeatedly.
func Schema() string { return schemaDDL }

// ApplySchema runs the DDL against d. Most deployments include this
// in their migration tool; the helper exists for fresh dev / test
// fixtures.
func ApplySchema(ctx context.Context, d *db.DB) error {
	if d == nil {
		return xerrs.Validation(CodeNilDB, "auditpg: nil DB")
	}
	if _, err := d.Exec(ctx, schemaDDL); err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeTransport,
			"auditpg: apply schema failed")
	}
	return nil
}

// Store is the Postgres-backed [audit.Store]. Construct with [New].
// Goroutine-safe.
//
// When hash-chain mode is in use (caller passed audit.WithHashChain
// to audit.New), Append takes a db/lock advisory lock keyed by
// "audit:chain" + serviceName so two processes can't fork the chain.
// The lock-name uses the first event's ServiceName — single-service
// deployments collide on a single advisory key, which is the
// intended behaviour.
type Store struct {
	d        *db.DB
	chainKey string
}

// New wraps an existing *db.DB. The store assumes [ApplySchema] or
// equivalent migration has already run.
func New(d *db.DB) *Store {
	return &Store{d: d, chainKey: "audit:chain"}
}

const appendSQL = `
INSERT INTO audit_events (
    id, occurred_at, service_name,
    actor_subject, actor_type, actor_ip, actor_ua,
    action, target_type, target_id, target_name,
    outcome, metadata, prev_hash, hash
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb, $14, $15
)`

// Append inserts e. Hash-chain serialization is the caller's
// responsibility via [Store.ChainLock]; Append itself does NOT
// acquire any lock so callers MUST take ChainLock first when
// running in chain mode.
func (s *Store) Append(ctx context.Context, e *audit.Event) error {
	if s == nil || s.d == nil {
		return xerrs.Validation(CodeNilDB, "auditpg: nil DB")
	}
	metaJSON, err := encodeMetadata(e.Metadata)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindInternal, CodeTransport,
			"auditpg: metadata encode failed")
	}
	if _, err := s.d.Exec(ctx, appendSQL,
		e.ID, e.OccurredAt, nullable(e.ServiceName),
		nullable(e.Actor.Subject), nullable(e.Actor.Type),
		nullable(e.Actor.IP), nullable(e.Actor.UA),
		e.Action, nullable(e.Target.Type),
		nullable(e.Target.ID), nullable(e.Target.Name),
		string(e.Outcome), metaJSON, e.PrevHash, e.Hash,
	); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodeTransport,
			"auditpg: insert failed")
	}
	return nil
}

const baseSelect = `
SELECT id, occurred_at, service_name,
       actor_subject, actor_type, actor_ip, actor_ua,
       action, target_type, target_id, target_name,
       outcome, metadata, prev_hash, hash
FROM audit_events
`

// Query reads events matching f, ordered by occurred_at ASC.
func (s *Store) Query(ctx context.Context, f audit.Filter) ([]audit.Event, error) {
	if s == nil || s.d == nil {
		return nil, xerrs.Validation(CodeNilDB, "auditpg: nil DB")
	}
	q, args := compileFilter(f)
	rows, err := s.d.Query(ctx, q, args...)
	if err != nil {
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeTransport,
			"auditpg: query failed")
	}
	defer rows.Close()
	var out []audit.Event
	for rows.Next() {
		var e audit.Event
		var serviceName, actorSubject, actorType, actorIP, actorUA, targetType, targetID, targetName *string
		var metaRaw []byte
		if err := rows.Scan(
			&e.ID, &e.OccurredAt, &serviceName,
			&actorSubject, &actorType, &actorIP, &actorUA,
			&e.Action, &targetType, &targetID, &targetName,
			&e.Outcome, &metaRaw, &e.PrevHash, &e.Hash,
		); err != nil {
			return nil, xerrs.Wrap(err, xerrs.KindInternal, CodeTransport,
				"auditpg: scan failed")
		}
		e.ServiceName = deref(serviceName)
		e.Actor = audit.Actor{
			Subject: deref(actorSubject), Type: deref(actorType),
			IP: deref(actorIP), UA: deref(actorUA),
		}
		e.Target = audit.Target{
			Type: deref(targetType), ID: deref(targetID),
			Name: deref(targetName),
		}
		if len(metaRaw) > 0 {
			var m map[string]any
			if err := json.Unmarshal(metaRaw, &m); err == nil {
				e.Metadata = m
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastHash returns the Hash of the most recent event (by
// occurred_at). Returns (nil, nil) on empty table.
func (s *Store) LastHash(ctx context.Context) ([]byte, error) {
	if s == nil || s.d == nil {
		return nil, xerrs.Validation(CodeNilDB, "auditpg: nil DB")
	}
	row := s.d.QueryRow(ctx, `
		SELECT hash FROM audit_events
		WHERE hash IS NOT NULL
		ORDER BY occurred_at DESC LIMIT 1
	`)
	var h []byte
	if err := row.Scan(&h); err != nil {
		// pgx returns ErrNoRows here on empty table; treat as
		// "no chain yet".
		if isNoRows(err) {
			return nil, nil
		}
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeTransport,
			"auditpg: last hash query failed")
	}
	return h, nil
}

// ChainLock acquires the cross-process advisory lock that
// serializes hash-chain writers. Postgres-side advisory lock keyed
// by [Store.chainKey]; release the lock by invoking the returned
// function.
func (s *Store) ChainLock(ctx context.Context) (func(), error) {
	if s == nil || s.d == nil {
		return func() {}, xerrs.Validation(CodeNilDB, "auditpg: nil DB")
	}
	lk := lock.New(s.d, s.chainKey)
	release, err := lk.Acquire(ctx)
	if err != nil {
		return func() {}, xerrs.Wrap(err, xerrs.KindUnavailable, CodeTransport,
			"auditpg: chain lock acquire failed")
	}
	return release, nil
}

// PurgeBefore deletes events with occurred_at < t.
func (s *Store) PurgeBefore(ctx context.Context, t time.Time) (int64, error) {
	if s == nil || s.d == nil {
		return 0, xerrs.Validation(CodeNilDB, "auditpg: nil DB")
	}
	tag, err := s.d.Exec(ctx, `DELETE FROM audit_events WHERE occurred_at < $1`, t)
	if err != nil {
		return 0, xerrs.Wrap(err, xerrs.KindUnavailable, CodeTransport,
			"auditpg: purge failed")
	}
	return tag.RowsAffected(), nil
}

func compileFilter(f audit.Filter) (string, []any) {
	var sb strings.Builder
	sb.WriteString(baseSelect)
	var clauses []string
	var args []any
	idx := 1
	if f.Actor != "" {
		clauses = append(clauses, fmt.Sprintf("actor_subject = $%d", idx))
		args = append(args, f.Actor)
		idx++
	}
	if f.Action != "" {
		if strings.HasSuffix(f.Action, "*") {
			pat := strings.TrimSuffix(f.Action, "*") + "%"
			clauses = append(clauses, fmt.Sprintf("action LIKE $%d", idx))
			args = append(args, pat)
		} else {
			clauses = append(clauses, fmt.Sprintf("action = $%d", idx))
			args = append(args, f.Action)
		}
		idx++
	}
	if f.TargetType != "" {
		clauses = append(clauses, fmt.Sprintf("target_type = $%d", idx))
		args = append(args, f.TargetType)
		idx++
	}
	if f.TargetID != "" {
		clauses = append(clauses, fmt.Sprintf("target_id = $%d", idx))
		args = append(args, f.TargetID)
		idx++
	}
	if f.Outcome != "" {
		clauses = append(clauses, fmt.Sprintf("outcome = $%d", idx))
		args = append(args, string(f.Outcome))
		idx++
	}
	if !f.From.IsZero() {
		clauses = append(clauses, fmt.Sprintf("occurred_at >= $%d", idx))
		args = append(args, f.From)
		idx++
	}
	if !f.To.IsZero() {
		clauses = append(clauses, fmt.Sprintf("occurred_at <= $%d", idx))
		args = append(args, f.To)
		// no idx++ here: this is the last positional placeholder, and
		// LIMIT/OFFSET below are inlined as literals, not args.
	}
	if len(clauses) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(clauses, " AND "))
	}
	sb.WriteString(" ORDER BY occurred_at ASC")
	if f.Limit > 0 {
		sb.WriteString(fmt.Sprintf(" LIMIT %d", f.Limit))
	}
	if f.Offset > 0 {
		sb.WriteString(fmt.Sprintf(" OFFSET %d", f.Offset))
	}
	return sb.String(), args
}

// encodeMetadata returns a JSON byte slice or nil for empty maps.
func encodeMetadata(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return nil, nil
	}
	return json.Marshal(m)
}

// nullable returns *string for INSERTs so empty strings land as NULL,
// keeping the table queryable with `WHERE col IS NULL`.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// isNoRows reports the pgx-no-rows sentinel without importing pgx
// here directly (db package already wraps it cleanly elsewhere; we
// match on string for a minimal-footprint check).
func isNoRows(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no rows")
}
