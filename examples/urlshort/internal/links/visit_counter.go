package links

import (
	"context"
	"log/slog"

	sq "github.com/Masterminds/squirrel"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/sqb"

	"github.com/theizzatbek/gokit/examples/urlshort/internal/events"
)

// VisitCounter is the NATS subscriber that persists visit counts.
// The Redirect handler returns immediately after publishing
// urlshort.link.visited; this struct's Handle method consumes those
// events and runs the UPDATE that bumps visit_count +
// last_visited_at out of the request path.
//
// Wired in main.go via:
//
//	service.WithNATSMapRegistration(func(e *natsmap.Engine) {
//	    vc := links.NewVisitCounter(svc.DB, svc.Logger())
//	    natsmap.RegisterHandler[events.LinkVisited](e, "link_visit_counter", vc.Handle)
//	})
//
// subscribers.yaml declares the matching subject + queue-group so a
// multi-replica deployment fans out (one of N replicas wins each
// event) rather than fans in (all replicas double-count).
//
// Durability: JetStream persists every published event before the
// publisher's call returns. If the subscriber goroutine crashes
// mid-update, JetStream redelivers on restart. If the UPDATE itself
// errors, returning a non-nil error here triggers a Nak; natsmap's
// auto-backoff schedules a redelivery. Counts are eventually
// consistent but never lost.
type VisitCounter struct {
	db  *db.DB
	log *slog.Logger
}

// NewVisitCounter wires the dependencies. log == nil falls back to
// slog.Default at call time.
func NewVisitCounter(d *db.DB, log *slog.Logger) *VisitCounter {
	return &VisitCounter{db: d, log: log}
}

// Handle is the natsmap-compatible signature. Runs a single
// idempotent-ish UPDATE per event: visit_count := visit_count + 1
// can over-count under at-least-once redelivery (NATS' default), but
// for a click counter that's preferable to under-counting; if you
// need exact-once, add an idempotency-key column to the schema and
// track event IDs (out of scope for this example).
func (v *VisitCounter) Handle(ctx context.Context, m natsclient.Msg[events.LinkVisited]) error {
	e := m.Data
	tag, err := sqb.Exec(ctx, v.db, sqb.Builder.
		Update("links").
		Set("visit_count", sq.Expr("visit_count + 1")).
		Set("last_visited_at", e.VisitedAt).
		Where(sq.Eq{"code": e.Code}))
	if err != nil {
		if v.log != nil {
			v.log.Warn("urlshort visit counter: update failed",
				"code", e.Code, "err", err.Error())
		}
		return err
	}
	if tag.RowsAffected() == 0 && v.log != nil {
		// Link was deleted between publish and consume. Treat as
		// success (no row to update) so the event isn't retried
		// forever.
		v.log.Info("urlshort visit counter: code missing on update",
			"code", e.Code)
	}
	return nil
}
