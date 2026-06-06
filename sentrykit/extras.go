package sentrykit

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
)

// ── A. Stats ──────────────────────────────────────────────────────────

// internalStats accumulates kit-side counters across the process.
// Fields are atomic for lock-free reads from /admin endpoints.
type internalStats struct {
	eventsCaptured    atomic.Int64
	eventsDeduped     atomic.Int64
	eventsRateLimited atomic.Int64
	breadcrumbs       atomic.Int64
	dedupeCacheSize   atomic.Int64
}

// statsGlobal is the process-wide counter set used by SlogHandler +
// FiberMiddleware + RecoverGo. Exposed via [Stats].
var statsGlobal internalStats

// Stats is the cheap point-in-time snapshot returned by [Stats].
// Suitable for /admin or /healthz endpoints.
type Stats struct {
	// EventsCaptured — number of records shipped as Sentry events
	// (Exception or Message) across the process lifetime.
	EventsCaptured int64

	// EventsDeduped — number of records that would have shipped as
	// events but were suppressed by the time-window dedupe cache.
	EventsDeduped int64

	// EventsRateLimited — number of records suppressed by the
	// fingerprint rate limiter ([WithCaptureRateLimit]).
	EventsRateLimited int64

	// BreadcrumbsEmitted — total breadcrumbs added across all hubs.
	BreadcrumbsEmitted int64

	// DedupeCacheSize — approximate number of distinct fingerprints
	// currently tracked in the dedupe cache.
	DedupeCacheSize int64
}

// GetStats returns the process-wide sentrykit counters. nil-safe in
// the sense that the underlying counters are always initialised at
// package load time.
func GetStats() Stats {
	return Stats{
		EventsCaptured:     statsGlobal.eventsCaptured.Load(),
		EventsDeduped:      statsGlobal.eventsDeduped.Load(),
		EventsRateLimited:  statsGlobal.eventsRateLimited.Load(),
		BreadcrumbsEmitted: statsGlobal.breadcrumbs.Load(),
		DedupeCacheSize:    statsGlobal.dedupeCacheSize.Load(),
	}
}

// ── B. WithCaptureRateLimit ──────────────────────────────────────────

// WithCaptureRateLimit caps the number of Sentry events shipped per
// minute per unique fingerprint (level + category + message). When
// the cap fires the record is recorded as a breadcrumb but the event
// is suppressed; the [Stats.EventsRateLimited] counter increments.
//
// Use alongside [WithCaptureLevel] to bound spend on the noisiest
// log lines: validation failures, retry-loop logs, etc. 0 disables
// (current behaviour — dedupe alone protects volume).
//
// The limiter is in-process; multi-replica deployments will count
// per pod, so set the cap with replication in mind.
func WithCaptureRateLimit(maxPerMin int) HandlerOption {
	return func(c *handlerConfig) { c.captureRateLimit = maxPerMin }
}

// rateLimitState is the per-fingerprint sliding-window counter
// shared across handler clones. Lives on slogHandler via a pointer
// so WithAttrs / WithGroup clones see the same limiter state.
type rateLimitState struct {
	mu   sync.Mutex
	hits map[string]*rateLimitBucket
}

type rateLimitBucket struct {
	count    int
	windowAt time.Time
}

func newRateLimitState() *rateLimitState {
	return &rateLimitState{hits: map[string]*rateLimitBucket{}}
}

// shouldEmit returns true when the fingerprint is below the per-minute
// cap. Sliding 60-second window — entries reset when the window
// rolls over.
func (s *rateLimitState) shouldEmit(fp string, max int, now time.Time) bool {
	if max <= 0 {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.hits[fp]
	if !ok || now.Sub(b.windowAt) >= time.Minute {
		s.hits[fp] = &rateLimitBucket{count: 1, windowAt: now}
		return true
	}
	if b.count >= max {
		return false
	}
	b.count++
	return true
}

// ── C. WithRouteFilter ──────────────────────────────────────────────

// fiberMiddlewareConfig configures [FiberMiddlewareWithOptions]. The
// existing [FiberMiddleware] constructor uses defaults.
type fiberMiddlewareConfig struct {
	skipFn func(path string) bool
}

// FiberMiddlewareOption configures [FiberMiddlewareWithOptions].
type FiberMiddlewareOption func(*fiberMiddlewareConfig)

// WithRouteFilter sets a predicate consulted on every request — when
// it returns true, the middleware does NOT clone the hub or
// populate scope; the request proceeds through the chain unchanged.
// Use to keep noisy health-check / metrics traffic out of Sentry.
//
// Default predicate (when this option is not supplied to
// [FiberMiddlewareWithOptions]): skip `/healthz`, `/readyz`,
// `/metrics`, `/favicon.ico`. [FiberMiddleware] itself does NOT
// install a default — it stays back-compat (tracks every request).
func WithRouteFilter(fn func(path string) bool) FiberMiddlewareOption {
	return func(c *fiberMiddlewareConfig) { c.skipFn = fn }
}

// DefaultRouteSkipFn skips the canonical observability paths. Pass
// this to [WithRouteFilter] to suppress Sentry overhead on
// k8s-probed / Prometheus-scraped routes.
func DefaultRouteSkipFn(path string) bool {
	switch path {
	case "/healthz", "/readyz", "/metrics", "/favicon.ico":
		return true
	}
	return false
}

// ── D. RecoverGo helper ─────────────────────────────────────────────

// RecoverGo wraps fn with a recover that captures the panic to the
// process-global Sentry hub and logs Warn to slog.Default. Use as the
// canonical "fire-and-forget goroutine" pattern when you can't tie
// the panic to a request lifecycle:
//
//	go sentrykit.RecoverGo(func() { backgroundWork() })
//
// The panic is captured (Sentry sees stack frames from the running
// goroutine) and then swallowed — the goroutine exits cleanly.
// Returns no value because the typical call is in a `go` statement
// where a return value would be discarded anyway.
//
// When sentrykit.Setup hasn't run the capture path no-ops; the slog
// Warn still fires so the panic is at least logged.
func RecoverGo(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			hub := sentry.CurrentHub()
			if hub != nil && hub.Client() != nil {
				hub.RecoverWithContext(context.Background(), r)
			}
			slog.Default().Warn("sentrykit: goroutine panic recovered",
				"panic", fmt.Sprint(r))
		}
	}()
	fn()
}

// ── F. AddBreadcrumb helper ────────────────────────────────────────

// AddBreadcrumb is a convenience over the hub resolved from ctx. Use
// inside handlers for explicit timeline entries beyond what the slog
// pipeline emits:
//
//	sentrykit.AddBreadcrumb(ctx, "billing", "charge submitted",
//	    map[string]any{"amount": 1999})
func AddBreadcrumb(ctx context.Context, category, message string, data map[string]any) {
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		hub = sentry.CurrentHub()
	}
	if hub == nil {
		return
	}
	hub.AddBreadcrumb(&sentry.Breadcrumb{
		Type:      "default",
		Category:  category,
		Message:   message,
		Data:      data,
		Level:     sentry.LevelInfo,
		Timestamp: time.Now(),
	}, nil)
	statsGlobal.breadcrumbs.Add(1)
}

// ── G. SetUser helper ──────────────────────────────────────────────

// SetUser stamps the per-request user attribution on the hub
// resolved from ctx. Use right after auth populates the principal:
//
//	if p, ok := auth.From[Claims](c); ok {
//	    sentrykit.SetUser(c.UserContext(), p.Subject, p.Custom.Email)
//	}
//
// email may be empty (some auth shapes don't carry it). When both
// are empty the call is a no-op — Sentry rejects User events with
// no identifier anyway.
func SetUser(ctx context.Context, id, email string) {
	if id == "" && email == "" {
		return
	}
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		hub = sentry.CurrentHub()
	}
	if hub == nil {
		return
	}
	hub.Scope().SetUser(sentry.User{ID: id, Email: email})
}

// ── H. WithoutPII / ScrubPII ───────────────────────────────────────

// piiQueryPattern matches common credential / token query params for
// scrubbing inside request URLs. Case-insensitive.
var piiQueryPattern = regexp.MustCompile(`(?i)(token|secret|password|key|api_key|access_token|refresh_token)=([^&]*)`)

// piiHeaderKeys lists request headers whose values must be replaced
// with "[redacted]" by [ScrubPII]. Lowercased canonical form;
// matched case-insensitively against the actual header name.
var piiHeaderKeys = map[string]struct{}{
	"authorization": {},
	"cookie":        {},
	"x-api-key":     {},
	"set-cookie":    {},
}

// ScrubPII returns a BeforeSend hook that:
//
//   - Replaces sensitive request header values (`Authorization`,
//     `Cookie`, `X-API-Key`, `Set-Cookie`) with "[redacted]".
//   - Strips secret-like query parameters (`token`, `password`,
//     `secret`, `api_key`, etc) from the request URL.
//
// Other event fields are left unchanged. Compose with
// [WithBeforeSend] to add app-specific scrubbing on top, or use the
// convenience [WithoutPII] which installs this hook as the BeforeSend
// directly.
//
// Server-side PII rules in the Sentry project remain the
// authoritative redaction layer — this is in-process defence in
// depth.
func ScrubPII() func(*sentry.Event, *sentry.EventHint) *sentry.Event {
	return func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
		if e == nil {
			return nil
		}
		if e.Request != nil {
			scrubRequestHeaders(e.Request.Headers)
			if e.Request.URL != "" {
				e.Request.URL = scrubURL(e.Request.URL)
			}
			if e.Request.QueryString != "" {
				e.Request.QueryString = piiQueryPattern.ReplaceAllString(e.Request.QueryString, "$1=[redacted]")
			}
		}
		return e
	}
}

// WithoutPII is the shortcut that installs [ScrubPII] as the
// BeforeSend hook. Compose with explicit [WithBeforeSend] only when
// you need to chain scrubbing with app-specific event mutation —
// otherwise this option is the one-liner.
func WithoutPII() Option {
	return WithBeforeSend(ScrubPII())
}

func scrubRequestHeaders(h map[string]string) {
	if h == nil {
		return
	}
	for k := range h {
		if _, ok := piiHeaderKeys[strings.ToLower(k)]; ok {
			h[k] = "[redacted]"
		}
	}
}

func scrubURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return piiQueryPattern.ReplaceAllString(raw, "$1=[redacted]")
	}
	if u.RawQuery == "" {
		return raw
	}
	u.RawQuery = piiQueryPattern.ReplaceAllString(u.RawQuery, "$1=[redacted]")
	return u.String()
}
