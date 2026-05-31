// Package events publishes link lifecycle events via natsmap. Failures
// are logged but never returned — analytics never blocks the foreground
// operation.
package events

import (
	"context"
	"log/slog"
	"time"

	"github.com/theizzatbek/gokit/clients/natsmap"
)

// Publisher / subscriber names used across the urlshort sample.
// Centralised here so handler/main/outbox-worker bindings can't
// drift from one another by a typo.
const (
	SubjectLinkCreated = "urlshort.link.created"
	SubjectLinkVisited = "urlshort.link.visited"
)

// LinkCreated payload published on urlshort.link.created.
type LinkCreated struct {
	LinkID    string    `json:"link_id"`
	UserID    string    `json:"user_id"`
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}

// LinkVisited payload published on urlshort.link.visited.
type LinkVisited struct {
	Code      string    `json:"code"`
	VisitedAt time.Time `json:"visited_at"`
	UserAgent string    `json:"user_agent,omitempty"`
	IP        string    `json:"ip,omitempty"`
}

// Publisher publishes link lifecycle events via natsmap. Best-effort:
// every method swallows publish failures (logs a Warn) so analytics
// never blocks the foreground request.
//
// Construct via NewPublisher; safe to embed in domain services that
// shouldn't know about natsmap directly.
type Publisher struct {
	rt  *natsmap.Runtime
	log *slog.Logger
}

// NewPublisher wires the natsmap runtime + logger used by every method.
// log == nil falls back to slog.Default at call time.
func NewPublisher(rt *natsmap.Runtime, log *slog.Logger) *Publisher {
	return &Publisher{rt: rt, log: log}
}

// LinkVisited publishes e on urlshort.link.visited. Nil-receiver safe.
// LinkCreated has moved to the transactional outbox path — see
// links.Service.Create. The visit publish stays on the direct path
// because LinkVisited is fire-and-forget analytics: bounded loss on
// a node crash is acceptable, and the outbox storage cost (one INSERT
// per click) would dominate the redirect hot path's latency budget.
func (p *Publisher) LinkVisited(ctx context.Context, e LinkVisited) {
	if p == nil {
		return
	}
	log := p.log
	if log == nil {
		log = slog.Default()
	}
	if err := natsmap.Publish[LinkVisited](ctx, p.rt, SubjectLinkVisited, e); err != nil {
		log.Warn("urlshort events: publish visited failed", "code", e.Code, "err", err.Error())
	}
}
