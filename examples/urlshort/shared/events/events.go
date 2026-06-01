// Package events is the SHARED event-payload contract between
// urlshort's three services (api / counter / enricher). It owns:
//
//   - NATS subject names (so a single typo doesn't desync the mesh)
//   - LinkCreated / LinkVisited struct shapes (JSON-encoded over NATS)
//
// Publishers / subscribers are NOT here — each service constructs
// its own thin wrapper. Keeping the shared package tiny means no
// service drags in the kit dependencies it doesn't use (the
// counter / enricher don't need natsmap, the api doesn't need apimap,
// etc.).
package events

import "time"

// NATS subjects used across the urlshort sample. Centralised so
// api's publishers and counter / enricher's subscribers can't drift
// apart by a typo at compile time.
const (
	SubjectLinkCreated = "urlshort.link.created"
	SubjectLinkVisited = "urlshort.link.visited"
)

// LinkCreated payload published on urlshort.link.created (by the
// api service, through the transactional outbox) and consumed by
// the enricher (which fetches Microlink metadata + HTML title and
// UPDATEs the matching link row).
type LinkCreated struct {
	LinkID    string    `json:"link_id"`
	UserID    string    `json:"user_id"`
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}

// LinkVisited payload published on urlshort.link.visited (by the api
// service on every successful redirect, fire-and-forget — bounded
// loss on a node crash is fine) and consumed by the counter (which
// aggregates and runs one batched UPDATE per fetch window).
type LinkVisited struct {
	Code      string    `json:"code"`
	VisitedAt time.Time `json:"visited_at"`
	UserAgent string    `json:"user_agent,omitempty"`
	IP        string    `json:"ip,omitempty"`
}
