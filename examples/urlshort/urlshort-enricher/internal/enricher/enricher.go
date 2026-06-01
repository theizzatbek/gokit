// Package enricher is the NATS-subscriber side of urlshort's
// metadata fetching. Consumes urlshort.link.created, calls Microlink
// (description + image_url) + an open-client HTML fetch (title), and
// UPDATEs the matching link row.
//
// Best-effort: partial enrichment is normal — Microlink down → only
// title comes back; HTML fetch 5xx → only Microlink fields land. The
// row is never blocked from existing; the api inserts with empty
// metadata and this UPDATE backfills.
package enricher

import (
	"context"
	"log/slog"
	"time"

	sq "github.com/Masterminds/squirrel"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/db/sqb"

	"github.com/theizzatbek/gokit/examples/urlshort/shared/events"
	"github.com/theizzatbek/gokit/examples/urlshort/urlshort-enricher/internal/enrich"
)

// Enricher binds the fetcher to the DB so the NATS handler closure
// can pull both via a single instance.
type Enricher struct {
	db      *db.DB
	fetcher *enrich.Fetcher
	log     *slog.Logger
}

// New wires the persistence + apimap fetcher.
func New(d *db.DB, fetcher *enrich.Fetcher, log *slog.Logger) *Enricher {
	if log == nil {
		log = slog.Default()
	}
	return &Enricher{db: d, fetcher: fetcher, log: log}
}

// Handle is the natsmap handler for one LinkCreated event. Returns
// nil on success (Ack); err triggers JetStream redelivery — kept
// rare since the underlying fetches are best-effort.
//
// Idempotent: re-running on the same event re-UPDATEs the same row
// with the same metadata. Stale-metadata races (api commit beats
// enricher to a backfill that another worker already did) are not
// a concern — the UPDATE replaces values regardless.
func (e *Enricher) Handle(ctx context.Context, msg natsclient.Msg[events.LinkCreated]) error {
	ev := msg.Data
	if ev.Code == "" {
		// Malformed payload — drop without retry.
		return nil
	}
	start := time.Now()
	title, description, imageURL := e.fetcher.FetchMetadata(ctx, ev.URL)
	if title == "" && description == "" && imageURL == "" {
		// Microlink down + HTML title parse failed — nothing to
		// update. Skip the DB hit; metadata stays empty until the
		// next event-replay or operator-driven backfill.
		e.log.Debug("urlshort enricher: nothing to set", "code", ev.Code, "url", ev.URL)
		return nil
	}
	_, err := sqb.Exec(ctx, e.db, sqb.Builder.
		Update("links").
		Set("title", title).
		Set("description", description).
		Set("image_url", imageURL).
		Where(sq.Eq{"code": ev.Code}))
	if err != nil {
		e.log.Warn("urlshort enricher: update failed",
			"code", ev.Code, "err", err.Error())
		return err
	}
	e.log.Debug("urlshort enricher: backfilled",
		"code", ev.Code, "elapsed", time.Since(start),
		"title_len", len(title), "desc_len", len(description),
		"image", imageURL != "")
	return nil
}
