package sentrykit

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
)

// HandlerOption configures [SlogHandler].
type HandlerOption func(*handlerConfig)

type handlerConfig struct {
	includeDebug   bool
	categoryAttr   string
	maxValueLen    int
	attrFilter     func(string) bool
	captureLevel   slog.Level // records >= this level capture as events (in addition to breadcrumb)
	captureEnabled bool       // tri-state: false = capture off entirely
	errorAttrKeys  []string   // attrs whose error-typed value drives CaptureException
	dedupeWindow   time.Duration
}

// WithDebugBreadcrumbs enables Debug-level records as breadcrumbs.
// Off by default because Debug is the level pgx's query tracer uses
// for every successful query — including all of them would flush the
// 100-item Sentry buffer in a single transactional handler.
func WithDebugBreadcrumbs() HandlerOption {
	return func(c *handlerConfig) { c.includeDebug = true }
}

// WithCategoryAttr names the slog attr key consulted for breadcrumb
// category. Default "category". When the attr is missing, the
// handler falls back to the first word of record.Message
// (lowercased), then "log" as a final default.
func WithCategoryAttr(key string) HandlerOption {
	return func(c *handlerConfig) { c.categoryAttr = key }
}

// WithMaxBreadcrumbValueLen caps individual attr-derived data values
// at n bytes (after stringification). Default 512. Lets ops keep
// breadcrumbs small enough that the event payload stays under
// Sentry's per-event size limits. n <= 0 disables the cap.
func WithMaxBreadcrumbValueLen(n int) HandlerOption {
	return func(c *handlerConfig) { c.maxValueLen = n }
}

// WithAttrFilter drops attr keys for which fn returns false. Default
// keeps every attr. Use to scrub noisy keys (raw SQL, large JSON
// blobs) without touching subsystem code.
func WithAttrFilter(fn func(key string) bool) HandlerOption {
	return func(c *handlerConfig) { c.attrFilter = fn }
}

// WithCaptureLevel enables Sentry event auto-capture for records at
// >= level. The record still becomes a breadcrumb on the hub —
// capture is additive, not a replacement. When an attr named by
// WithCaptureErrorAttrKeys carries an `error` value, the event is
// CaptureException (stack frames from the running goroutine);
// otherwise it's CaptureMessage with the record's text.
//
// Off by default (PR #1 contract: only panic + WrapErrorHandler
// auto-capture). Typical wiring:
//
//	sentrykit.SlogHandler(inner, sentrykit.WithCaptureLevel(slog.LevelError))
//
// Dedupe (WithCaptureDedupeWindow) protects high-volume Error logs
// from generating one event per call.
func WithCaptureLevel(level slog.Level) HandlerOption {
	return func(c *handlerConfig) {
		c.captureLevel = level
		c.captureEnabled = true
	}
}

// WithCaptureErrorAttrKeys overrides the list of attr keys consulted
// for an `error` value when promoting a captured record to a Sentry
// Exception. Default: ["err", "error", "cause"]. Order matters — the
// first match wins. Pass an empty slice to always use CaptureMessage.
func WithCaptureErrorAttrKeys(keys ...string) HandlerOption {
	return func(c *handlerConfig) {
		c.errorAttrKeys = append([]string(nil), keys...)
	}
}

// WithCaptureDedupeWindow caps event volume: a fingerprint of
// (level, category, message) seen within d of a previous occurrence
// is suppressed (the breadcrumb still adds, but no new event ships).
// Default 60s. Set d <= 0 to disable dedupe entirely — every
// captured record ships an event.
func WithCaptureDedupeWindow(d time.Duration) HandlerOption {
	return func(c *handlerConfig) { c.dedupeWindow = d }
}

// SlogHandler wraps inner so every record passed through it also
// becomes a Sentry breadcrumb on the hub resolved from the record's
// ctx (falls back to sentry.CurrentHub when ctx carries no hub).
//
// inner still receives every record — the breadcrumb is a pure side
// effect. Console / JSON log pipelines keep working unchanged.
//
// Level mapping:
//
//	Debug → skipped (unless WithDebugBreadcrumbs)
//	Info  → breadcrumb level "info"
//	Warn  → "warning"
//	Error → "error" (still breadcrumb only — CaptureException is
//	         a separate concern owned by service callers explicitly)
//
// Category is derived from the explicit attr named by
// WithCategoryAttr (default "category") if present; otherwise from
// the first word of record.Message lowercased; otherwise "log".
func SlogHandler(inner slog.Handler, opts ...HandlerOption) slog.Handler {
	cfg := &handlerConfig{
		categoryAttr:  "category",
		maxValueLen:   512,
		errorAttrKeys: []string{"err", "error", "cause"},
		dedupeWindow:  60 * time.Second,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	h := &slogHandler{inner: inner, cfg: cfg}
	if cfg.captureEnabled && cfg.dedupeWindow > 0 {
		h.dedupe = &dedupeCache{}
	}
	return h
}

// dedupeCache fingerprints captured records and suppresses
// duplicates seen within cfg.dedupeWindow. sync.Map keeps lookups
// lock-free in the hot path; lastSeen is a unix-nano stored as
// time.Time on insert.
//
// Entries never expire on their own — they're overwritten when a
// fingerprint repeats, and "old" entries just sit around. A single
// process running for weeks may accumulate up to one entry per
// distinct (level, category, message) tuple ever seen; that's bound
// by the actual code emitting Error logs, not by traffic volume, so
// the memory ceiling is small (hundreds of entries).
type dedupeCache struct {
	m sync.Map // fingerprint string → time.Time
}

func (d *dedupeCache) shouldEmit(fp string, now time.Time, window time.Duration) bool {
	if v, ok := d.m.Load(fp); ok {
		last := v.(time.Time)
		if now.Sub(last) < window {
			return false
		}
	}
	d.m.Store(fp, now)
	return true
}

type slogHandler struct {
	inner slog.Handler
	cfg   *handlerConfig
	// preAttrs are accumulated from WithAttrs, with keys already
	// prefixed by whatever group path was active at WithAttrs time.
	// That snapshots slog's "groups affect subsequent attrs" rule
	// without re-traversing the chain at Handle time.
	preAttrs []slog.Attr
	// groupPrefix is the dotted path imposed by every WithGroup call
	// so far (e.g. "event." or "event.payload."). Empty by default.
	// Record-time attrs and any future WithAttrs use this prefix.
	groupPrefix string
	// dedupe is non-nil iff WithCaptureLevel was set AND
	// dedupeWindow > 0. Shared across all WithAttrs/WithGroup clones
	// so dedupe state isn't per-logger-instance.
	dedupe *dedupeCache
}

func (h *slogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *slogHandler) Handle(ctx context.Context, r slog.Record) error {
	// Always deliver to inner first so the breadcrumb side effect
	// can't suppress the user's chosen log pipeline.
	innerErr := h.inner.Handle(ctx, r)

	if r.Level == slog.LevelDebug && !h.cfg.includeDebug {
		return innerErr
	}

	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		hub = sentry.CurrentHub()
	}
	if hub == nil {
		return innerErr
	}

	data := map[string]interface{}{}
	for _, a := range h.preAttrs {
		// preAttrs are stored with prefixes already applied; pass an
		// empty prefix here so they don't get double-prefixed.
		h.copyAttr(a, data, "")
	}
	r.Attrs(func(a slog.Attr) bool {
		h.copyAttr(a, data, h.groupPrefix)
		return true
	})

	category := h.resolveCategory(r, data)
	delete(data, h.cfg.categoryAttr) // don't duplicate in Data once promoted

	bc := &sentry.Breadcrumb{
		Type:      "default",
		Category:  category,
		Message:   r.Message,
		Level:     mapLevel(r.Level),
		Timestamp: r.Time,
	}
	if len(data) > 0 {
		bc.Data = data
	}
	if bc.Timestamp.IsZero() {
		bc.Timestamp = time.Now()
	}
	hub.AddBreadcrumb(bc, nil)

	// Capture as a Sentry event when the record is at or above the
	// configured threshold AND the dedupe cache lets it through.
	// Breadcrumb is always added first so even dedupe-suppressed
	// records still show up in any subsequent event's timeline.
	if h.cfg.captureEnabled && r.Level >= h.cfg.captureLevel {
		now := time.Now()
		fp := h.fingerprint(r.Level, category, r.Message)
		if h.dedupe == nil || h.dedupe.shouldEmit(fp, now, h.cfg.dedupeWindow) {
			h.captureEvent(hub, r, category, data)
		}
	}
	return innerErr
}

// captureEvent ships the record as a Sentry event. When an attr in
// h.cfg.errorAttrKeys carries an error value, the event is a typed
// Exception (Sentry renders stack frames from the running
// goroutine); otherwise it's a Message event with the record text.
func (h *slogHandler) captureEvent(hub *sentry.Hub, r slog.Record, category string, data map[string]interface{}) {
	hub.WithScope(func(scope *sentry.Scope) {
		// Tag the event with the resolved category so Sentry's
		// facets stay consistent between breadcrumb and event for
		// the same record.
		scope.SetTag("category", category)
		scope.SetLevel(mapLevel(r.Level))
		// Pack attrs into a single "log" context block so they
		// surface in the Sentry UI's contexts section without
		// inflating the tags facet (which is global cardinality).
		// Skip the err/error/cause keys we promoted to Exception —
		// duplicating the same error string is noise.
		ctx := sentry.Context{}
		for k, v := range data {
			if h.isErrorAttrKey(k) {
				continue
			}
			ctx[k] = v
		}
		if len(ctx) > 0 {
			scope.SetContext("log", ctx)
		}
		if err := h.extractError(r); err != nil {
			hub.CaptureException(err)
			return
		}
		hub.CaptureMessage(r.Message)
	})
}

// fingerprint produces a stable string for dedupe lookups. We
// deliberately avoid including attr values: a typical
// `logger.ErrorContext(ctx, "db query failed", "err", err)` should
// dedupe by (Error, "db", "db query failed"), not by the specific
// error string which often varies (timestamps, IDs).
func (h *slogHandler) fingerprint(level slog.Level, category, msg string) string {
	return level.String() + "|" + category + "|" + msg
}

// extractError returns the first error-typed value among
// h.cfg.errorAttrKeys present in the record's attrs (including
// preAttrs). Returns nil when none of the keys hold an error.
func (h *slogHandler) extractError(r slog.Record) error {
	if len(h.cfg.errorAttrKeys) == 0 {
		return nil
	}
	keys := h.cfg.errorAttrKeys
	// Walk preAttrs first, then record attrs. Last write wins —
	// preserves slog's per-call attr precedence.
	var found error
	for _, a := range h.preAttrs {
		if e, ok := matchErrorAttr(a, keys); ok {
			found = e
		}
	}
	r.Attrs(func(a slog.Attr) bool {
		if e, ok := matchErrorAttr(a, keys); ok {
			found = e
		}
		return true
	})
	return found
}

func matchErrorAttr(a slog.Attr, keys []string) (error, bool) {
	for _, k := range keys {
		if a.Key != k {
			continue
		}
		// Strip the group prefix slogHandler may have attached.
		// preAttrs keys arrive prefixed (e.g. "event.err"); skip
		// those — capture only matches top-level keys to avoid
		// ambiguous semantics inside nested groups.
		if i := strings.LastIndex(a.Key, "."); i >= 0 && a.Key != k {
			continue
		}
		if e, ok := a.Value.Resolve().Any().(error); ok {
			return e, true
		}
	}
	return nil, false
}

func (h *slogHandler) isErrorAttrKey(k string) bool {
	for _, want := range h.cfg.errorAttrKeys {
		if k == want {
			return true
		}
	}
	return false
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.inner = h.inner.WithAttrs(attrs)
	clone.dedupe = h.dedupe // shared cache across clones
	// Snapshot the attrs with the CURRENT group prefix applied so
	// later WithGroup calls don't retroactively re-prefix them.
	prefixed := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		prefixed[i] = slog.Attr{Key: h.groupPrefix + a.Key, Value: a.Value}
	}
	clone.preAttrs = append(append([]slog.Attr(nil), h.preAttrs...), prefixed...)
	return &clone
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.inner = h.inner.WithGroup(name)
	clone.dedupe = h.dedupe // shared cache across clones
	if name != "" {
		clone.groupPrefix = h.groupPrefix + name + "."
	}
	return &clone
}

// copyAttr writes one attr into data, applying the filter + value
// cap. Group attrs (slog.Group("name", ...)) are flattened with
// dot-prefixes (matching the JSON handler convention). prefix is the
// dotted group path accumulated via WithGroup that should be
// prepended to a.Key.
func (h *slogHandler) copyAttr(a slog.Attr, data map[string]interface{}, prefix string) {
	if h.cfg.attrFilter != nil && !h.cfg.attrFilter(a.Key) {
		return
	}
	key := prefix + a.Key
	val := a.Value.Resolve()
	if val.Kind() == slog.KindGroup {
		for _, sub := range val.Group() {
			h.copyAttr(sub, data, key+".")
		}
		return
	}
	data[key] = h.capValue(val.Any())
}

// capValue stringifies + truncates the value when maxValueLen > 0.
// Numbers / bools / time pass through as-is — Sentry's JSON encoder
// handles them natively, and they're never long enough to matter.
func (h *slogHandler) capValue(v any) any {
	if h.cfg.maxValueLen <= 0 {
		return v
	}
	switch tv := v.(type) {
	case string:
		if len(tv) > h.cfg.maxValueLen {
			return tv[:h.cfg.maxValueLen] + "…"
		}
		return tv
	case []byte:
		if len(tv) > h.cfg.maxValueLen {
			return string(tv[:h.cfg.maxValueLen]) + "…"
		}
		return string(tv)
	case error:
		s := tv.Error()
		if len(s) > h.cfg.maxValueLen {
			return s[:h.cfg.maxValueLen] + "…"
		}
		return s
	case fmt.Stringer:
		s := tv.String()
		if len(s) > h.cfg.maxValueLen {
			return s[:h.cfg.maxValueLen] + "…"
		}
		return s
	default:
		return v
	}
}

// resolveCategory picks the breadcrumb category in priority order:
//  1. explicit attr named by cfg.categoryAttr (case-sensitive, as
//     slog records keys verbatim).
//  2. first word of record.Message lowercased.
//  3. literal "log".
func (h *slogHandler) resolveCategory(r slog.Record, data map[string]interface{}) string {
	if v, ok := data[h.cfg.categoryAttr]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	if msg := strings.TrimSpace(r.Message); msg != "" {
		// First word; treat ':' as a separator too so "db:" style
		// prefixes work without a custom attr.
		i := strings.IndexAny(msg, " \t:")
		if i > 0 {
			return strings.ToLower(msg[:i])
		}
		return strings.ToLower(msg)
	}
	return "log"
}

// mapLevel translates slog.Level into the Sentry breadcrumb level
// enum. Warn ↦ "warning" matches the Sentry data model (not "warn").
func mapLevel(l slog.Level) sentry.Level {
	switch {
	case l >= slog.LevelError:
		return sentry.LevelError
	case l >= slog.LevelWarn:
		return sentry.LevelWarning
	case l >= slog.LevelInfo:
		return sentry.LevelInfo
	default:
		return sentry.LevelDebug
	}
}
