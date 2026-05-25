// Package events publishes link lifecycle events to NATS JetStream.
// Failures are logged but never returned — analytics never blocks the
// foreground operation.
package events

import (
	"context"
	"log/slog"
	"time"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
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

type Publishers struct {
	created *natsclient.Publisher[LinkCreated]
	visited *natsclient.Publisher[LinkVisited]
	log     *slog.Logger
}

func New(c *natsclient.Client, log *slog.Logger) *Publishers {
	if log == nil {
		log = slog.Default()
	}
	return &Publishers{
		created: natsclient.NewPublisher[LinkCreated](c),
		visited: natsclient.NewPublisher[LinkVisited](c),
		log:     log,
	}
}

// PublishCreated is best-effort.
func (p *Publishers) PublishCreated(ctx context.Context, e LinkCreated) {
	if err := p.created.Publish(ctx, "urlshort.link.created", e); err != nil {
		p.log.Warn("urlshort events: publish created failed", "code", e.Code, "err", err.Error())
	}
}

// PublishVisited is best-effort.
func (p *Publishers) PublishVisited(ctx context.Context, e LinkVisited) {
	if err := p.visited.Publish(ctx, "urlshort.link.visited", e); err != nil {
		p.log.Warn("urlshort events: publish visited failed", "code", e.Code, "err", err.Error())
	}
}
