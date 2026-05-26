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

// PublishCreated is best-effort. log == nil uses slog.Default.
func PublishCreated(ctx context.Context, rt *natsmap.Runtime, log *slog.Logger, e LinkCreated) {
	if log == nil {
		log = slog.Default()
	}
	if err := natsmap.Publish[LinkCreated](ctx, rt, "urlshort.link.created", e); err != nil {
		log.Warn("urlshort events: publish created failed", "code", e.Code, "err", err.Error())
	}
}

// PublishVisited is best-effort. log == nil uses slog.Default.
func PublishVisited(ctx context.Context, rt *natsmap.Runtime, log *slog.Logger, e LinkVisited) {
	if log == nil {
		log = slog.Default()
	}
	if err := natsmap.Publish[LinkVisited](ctx, rt, "urlshort.link.visited", e); err != nil {
		log.Warn("urlshort events: publish visited failed", "code", e.Code, "err", err.Error())
	}
}
