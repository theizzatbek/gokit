package links

import (
	"context"
	"log/slog"
	"time"

	sq "github.com/Masterminds/squirrel"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/sqb"

	"github.com/theizzatbek/gokit/examples/urlshort/internal/events"
)

// VisitCounter is the batched NATS subscriber that persists visit
// counts. Wired through natsmap.RegisterBatchedHandler against a
// subscriber declared with `batch_size: N` in subscribers.yaml.
//
// Per delivery:
//
//  1. natsmap pulls up to BatchSize messages with a deadline of
//     BatchInterval and hands them to Handle as one slice.
//  2. Handle aggregates the events in-memory per code (domain
//     decision — a single visit_count bump on a popular code is
//     one row, not N).
//  3. Handle runs ONE `UPDATE … FROM (VALUES …)` against Postgres.
//  4. On nil return → natsmap Acks every message in the batch.
//     On non-nil return → natsmap Naks every message → JetStream
//     redelivers the whole batch on the next fetch.
//
// All-or-nothing: the DB UPDATE and the per-message ack live on the
// same "did the batch succeed?" boolean. No partial commits.
type VisitCounter struct {
	db  *db.DB
	log *slog.Logger
}

type visitAgg struct {
	delta  int64
	lastTS time.Time
}

// NewVisitCounter wires the persistence side. The subscription
// lifecycle is owned by natsmap — Drain it via natsmap.Runtime.Drain
// (service.Close already does this before the DB pool tears down).
func NewVisitCounter(d *db.DB, log *slog.Logger) *VisitCounter {
	return &VisitCounter{db: d, log: log}
}

// Handle receives one batch from natsmap's pull subscriber. Returns
// nil on success (natsmap Acks all) or err on failure (natsmap Naks
// all → redelivery).
func (vc *VisitCounter) Handle(ctx context.Context, batch []natsclient.Msg[events.LinkVisited]) error {
	if len(batch) == 0 {
		return nil
	}
	agg := make(map[string]visitAgg, len(batch))
	for _, m := range batch {
		e := m.Data
		a := agg[e.Code] // zero-value when absent
		a.delta++
		if e.VisitedAt.After(a.lastTS) {
			a.lastTS = e.VisitedAt
		}
		agg[e.Code] = a
	}
	if _, err := sqb.Exec(ctx, vc.db, buildVisitUpdate(agg)); err != nil {
		if vc.log != nil {
			vc.log.Warn("urlshort visit counter: batch update failed",
				"batch_codes", len(agg), "batch_events", len(batch), "err", err.Error())
		}
		return err
	}
	if vc.log != nil {
		var total int64
		for _, a := range agg {
			total += a.delta
		}
		vc.log.Debug("urlshort visit counter: batch persisted",
			"codes", len(agg), "events", len(batch), "visits", total)
	}
	return nil
}

// buildVisitUpdate composes the batched-increment query:
//
//	UPDATE links AS l
//	SET visit_count    = l.visit_count + v.delta,
//	    last_visited_at = greatest(coalesce(l.last_visited_at, 'epoch'::timestamptz), v.ts)
//	FROM unnest($1::text[], $2::bigint[], $3::timestamptz[]) AS v(code, delta, ts)
//	WHERE l.code = v.code
//
// `unnest` keeps the parameter count fixed at three regardless of
// batch size — one text[], one bigint[], one timestamptz[]. pgx
// binds the Go slices natively. The trade vs. multi-row VALUES is
// (a) no per-row stringbuilding, (b) no first-row type-cast
// asymmetry, (c) the prepared statement plan stays stable since
// the parameter shape never changes.
func buildVisitUpdate(agg map[string]visitAgg) sq.UpdateBuilder {
	codes := make([]string, 0, len(agg))
	deltas := make([]int64, 0, len(agg))
	timestamps := make([]time.Time, 0, len(agg))
	for code, a := range agg {
		codes = append(codes, code)
		deltas = append(deltas, a.delta)
		timestamps = append(timestamps, a.lastTS)
	}
	return sqb.Builder.
		Update("links AS l").
		Set("visit_count", sq.Expr("l.visit_count + v.delta")).
		Set("last_visited_at", sq.Expr(
			"greatest(coalesce(l.last_visited_at, 'epoch'::timestamptz), v.ts)")).
		Suffix(
			"FROM unnest(?::text[], ?::bigint[], ?::timestamptz[]) AS v(code, delta, ts) "+
				"WHERE l.code = v.code",
			codes, deltas, timestamps,
		)
}
