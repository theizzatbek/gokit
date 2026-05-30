package sentrykit

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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
		categoryAttr: "category",
		maxValueLen:  512,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return &slogHandler{inner: inner, cfg: cfg}
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
	return innerErr
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.inner = h.inner.WithAttrs(attrs)
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
//   1. explicit attr named by cfg.categoryAttr (case-sensitive, as
//      slog records keys verbatim).
//   2. first word of record.Message lowercased.
//   3. literal "log".
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