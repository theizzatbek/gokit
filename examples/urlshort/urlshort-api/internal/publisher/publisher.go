// Package publisher is api-side wiring for the LinkVisited HTTP
// publish.
//
// urlshort-api does NOT talk to NATS directly. LinkVisited is sent
// as a raw JSON body to urlshort-publisher's POST /publish/:subject
// endpoint (the kit's natsmap/natsgw primitive), which republishes
// onto the matching NATS subject. The latency cost is one
// in-cluster HTTP RTT (~1-5ms); the win is that the api binary has
// no natsmap import surface — easier to deploy into network zones
// without NATS reachability.
//
// LinkCreated goes through the transactional outbox (writes inside
// the same db.Tx as the link insert) so it does NOT live here. See
// links.Service.Create.
package publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/theizzatbek/gokit/examples/urlshort/shared/events"
)

// Visit calls urlshort-publisher's POST /publish/:subject endpoint
// for LinkVisited events. Best-effort: HTTP failures (publisher
// down, 5xx, timeout) are logged at Warn and swallowed so the
// redirect hot path is never blocked.
//
// PublisherURL is the publisher's base URL (e.g. http://publisher:3001).
// Client is the HTTP client to use — wire a *http.Client built
// through gokit/clients/httpc so retries + timeouts ride the kit's
// transport chain.
type Visit struct {
	publisherURL string
	client       *http.Client
	log          *slog.Logger
}

// NewVisit wires the HTTP-side Publisher.
//
// publisherURL == "" returns a no-op Publisher (every method
// silently drops) so unit tests don't need to spin a fake server.
// client may be nil — falls back to http.DefaultClient.
func NewVisit(publisherURL string, client *http.Client, log *slog.Logger) *Visit {
	if log == nil {
		log = slog.Default()
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &Visit{publisherURL: publisherURL, client: client, log: log}
}

// LinkVisited POSTs e to the publisher gateway. Nil-receiver safe
// — useful for unit tests that don't wire a publisher URL.
func (p *Visit) LinkVisited(ctx context.Context, e events.LinkVisited) {
	if p == nil || p.publisherURL == "" {
		return
	}
	body, err := json.Marshal(e)
	if err != nil {
		p.log.Warn("urlshort api: encode visit failed",
			"code", e.Code, "err", err.Error())
		return
	}
	url := p.publisherURL + "/publish/" + events.SubjectLinkVisited
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		p.log.Warn("urlshort api: build publish req failed",
			"code", e.Code, "err", err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		p.log.Warn("urlshort api: publish visited failed",
			"code", e.Code, "err", err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		p.log.Warn("urlshort api: publish visited rejected",
			"code", e.Code, "status", resp.StatusCode)
	}
}
