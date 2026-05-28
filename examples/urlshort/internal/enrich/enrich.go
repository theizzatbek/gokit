// Package enrich fetches title + description + image_url for a URL,
// using apimap for both the MicroLink lookup AND the open-client
// title fetch. All calls are best-effort: partial results are normal,
// errors are swallowed and logged at Debug.
package enrich

import (
	"bytes"
	"context"
	"log/slog"
	"net/url"

	"github.com/theizzatbek/gokit/clients/apimap"
)

// MicroLinkResp is the slice of MicroLink's response we care about.
type MicroLinkResp struct {
	Status string `json:"status"`
	Data   struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Image       struct {
			URL string `json:"url"`
		} `json:"image"`
	} `json:"data"`
}

type Fetcher struct {
	apiClient *apimap.Client
	logger    *slog.Logger
}

// NewFetcher takes the apimap client wired with two endpoints:
//
//	microlink.metadata — declared base_url, /  (JSON response)
//	web.fetch          — open client (no base_url, full URL via Call.URL)
//
// See examples/urlshort/clients.yaml for the YAML side.
func NewFetcher(apiClient *apimap.Client, log *slog.Logger) *Fetcher {
	if log == nil {
		log = slog.Default()
	}
	return &Fetcher{apiClient: apiClient, logger: log}
}

// FetchMetadata returns title, description, image_url. Never errors.
// Partial fields are normal — if MicroLink is down we still return
// whatever the open-client fetch could grab from <title>.
func (f *Fetcher) FetchMetadata(ctx context.Context, target string) (title, description, imageURL string) {
	// 1. Open-client fetch: GET arbitrary target, parse <title> from HTML.
	body, err := apimap.Decode[[]byte](ctx, f.apiClient, "web.fetch", apimap.Call{URL: target})
	if err != nil {
		f.logger.Debug("urlshort enrich: web fetch failed", "url", target, "err", err.Error())
	} else {
		title = parseTitle(bytes.NewReader(body))
	}

	// 2. MicroLink for description + image.
	out, err := apimap.Decode[MicroLinkResp](ctx, f.apiClient, "microlink.metadata",
		apimap.Call{Query: url.Values{"url": []string{target}}})
	if err != nil {
		f.logger.Debug("urlshort enrich: microlink failed", "url", target, "err", err.Error())
		return title, description, imageURL
	}
	if out.Status != "success" {
		return title, description, imageURL
	}
	if title == "" {
		title = out.Data.Title
	}
	description = out.Data.Description
	imageURL = out.Data.Image.URL
	return title, description, imageURL
}
