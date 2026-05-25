// Package enrich fetches title + description + image_url for a URL,
// using httpc for arbitrary fetch + apimap for the MicroLink call.
// All calls are best-effort: partial results are normal, errors are
// swallowed and logged at Debug.
package enrich

import (
	"context"
	"log/slog"
	"net/http"
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
	httpClient *http.Client
	apiClient  *apimap.Client
	logger     *slog.Logger
}

func NewFetcher(httpClient *http.Client, apiClient *apimap.Client, log *slog.Logger) *Fetcher {
	if log == nil {
		log = slog.Default()
	}
	return &Fetcher{httpClient: httpClient, apiClient: apiClient, logger: log}
}

// FetchMetadata returns title, description, image_url. Never errors.
// Partial fields are normal — if MicroLink is down we still return
// whatever httpc could grab from <title>.
func (f *Fetcher) FetchMetadata(ctx context.Context, target string) (title, description, imageURL string) {
	// 1. httpc: fetch HTML, parse <title>.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err == nil {
		if resp, err := f.httpClient.Do(req); err == nil {
			title = parseTitle(resp.Body)
			_ = resp.Body.Close()
		} else {
			f.logger.Debug("urlshort enrich: httpc fetch failed", "url", target, "err", err.Error())
		}
	}

	// 2. apimap: MicroLink for description + image.
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
