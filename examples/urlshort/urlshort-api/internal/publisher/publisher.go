// Package publisher is api-side wiring for the LinkVisited NATS
// publish. LinkCreated goes through the transactional outbox (writes
// inside the same db.Tx as the link insert) so it does NOT live here
// — see links.Service.Create.
//
// LinkVisited is fire-and-forget analytics: bounded loss on a node
// crash is acceptable, and the outbox storage cost (one INSERT per
// click) would dominate the redirect hot path's latency budget.
package publisher

import (
	"context"
	"log/slog"

	"github.com/theizzatbek/gokit/clients/natsmap"

	"github.com/theizzatbek/gokit/examples/urlshort/shared/events"
)

// Visit publishes urlshort.link.visited via natsmap. Best-effort:
// every method swallows publish failures (logs a Warn) so analytics
// never blocks the foreground request.
type Visit struct {
	rt  *natsmap.Runtime
	log *slog.Logger
}

// NewVisit wires a thin Publisher over rt. nil rt → no-op Publisher
// (every method silently drops). nil logger → slog.Default.
func NewVisit(rt *natsmap.Runtime, log *slog.Logger) *Visit {
	if log == nil {
		log = slog.Default()
	}
	return &Visit{rt: rt, log: log}
}

// LinkVisited publishes e on the LinkVisited subject. Nil-receiver
// safe — useful for unit tests that don't wire a NATS runtime.
func (p *Visit) LinkVisited(ctx context.Context, e events.LinkVisited) {
	if p == nil || p.rt == nil {
		return
	}
	if err := natsmap.Publish[events.LinkVisited](ctx, p.rt, events.SubjectLinkVisited, e); err != nil {
		p.log.Warn("urlshort api: publish visited failed",
			"code", e.Code, "err", err.Error())
	}
}
