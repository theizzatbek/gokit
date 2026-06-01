package auditadmin

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/audit"
)

// Mount registers two handlers on app under base:
//
//	GET <base>          → HTML page with filter form + result table
//	GET <base>.json     → same Filter, returns Events as JSON
//
// Both honour the same query-string params:
//
//	actor=<subject>         exact match on Actor.Subject
//	action=<pattern>        action filter; supports trailing "*"
//	outcome=<success|failure|denied>
//	target_type=<resource>
//	target_id=<id>
//	from=<rfc3339>          inclusive
//	to=<rfc3339>            inclusive
//	limit=<int>             default 50, max 500
//	offset=<int>            default 0
//
// Callers wire their own auth middleware in front of app.Use(base)
// before this Mount call; the package deliberately doesn't ship its
// own auth — different services have different role conventions.
func Mount(app fiber.Router, base string, logger *audit.Logger) {
	base = strings.TrimRight(base, "/")
	app.Get(base, htmlHandler(logger, base))
	app.Get(base+".json", jsonHandler(logger))
}

// MountOnEngine is a convenience for service-side wiring. Same
// semantics as [Mount] but accepts a fiber.App directly so the
// signature is symmetric with auth/fibermount helpers.
func MountOnEngine(app *fiber.App, base string, logger *audit.Logger) {
	Mount(app, base, logger)
}

func htmlHandler(logger *audit.Logger, base string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		f, err := parseFilter(c)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(err.Error())
		}
		events, qerr := logger.Query(c.UserContext(), f)
		c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
		return c.Send(renderHTML(c, base, f, events, qerr))
	}
}

func jsonHandler(logger *audit.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		f, err := parseFilter(c)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(map[string]string{"error": err.Error()})
		}
		events, err := logger.Query(c.UserContext(), f)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(map[string]string{"error": err.Error()})
		}
		// Set Content-Disposition so curl -OJ saves as a file.
		c.Set("Content-Disposition", `attachment; filename="audit-export.json"`)
		return c.JSON(events)
	}
}

const (
	defaultLimit = 50
	maxLimit     = 500
)

func parseFilter(c *fiber.Ctx) (audit.Filter, error) {
	f := audit.Filter{
		Actor:      c.Query("actor"),
		Action:     c.Query("action"),
		Outcome:    audit.Outcome(c.Query("outcome")),
		TargetType: c.Query("target_type"),
		TargetID:   c.Query("target_id"),
		Limit:      defaultLimit,
	}
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return f, fmt.Errorf("invalid limit %q", v)
		}
		if n > maxLimit {
			n = maxLimit
		}
		f.Limit = n
	}
	if v := c.Query("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return f, fmt.Errorf("invalid offset %q", v)
		}
		f.Offset = n
	}
	if v := c.Query("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, fmt.Errorf("invalid from %q (RFC3339)", v)
		}
		f.From = t
	}
	if v := c.Query("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, fmt.Errorf("invalid to %q (RFC3339)", v)
		}
		f.To = t
	}
	return f, nil
}

func renderHTML(c *fiber.Ctx, base string, f audit.Filter, events []audit.Event, qerr error) []byte {
	var sb strings.Builder
	sb.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>audit</title>`)
	sb.WriteString(`<style>
body { font: 13px/1.5 -apple-system, BlinkMacSystemFont, sans-serif; margin: 0; color: #1a1a1a; background: #fafafa; }
header { background: #111827; color: white; padding: 18px 32px; }
header h1 { margin: 0; font-size: 18px; }
form.filters { padding: 16px 32px; background: white; border-bottom: 1px solid #e5e7eb; display: flex; gap: 12px; flex-wrap: wrap; }
form.filters label { display: flex; flex-direction: column; font-size: 12px; color: #6b7280; }
form.filters input, form.filters select { font: 13px ui-monospace, SFMono-Regular, monospace; padding: 4px 8px; border: 1px solid #d1d5db; border-radius: 4px; min-width: 140px; }
form.filters button { align-self: flex-end; padding: 6px 16px; font-size: 13px; background: #2563eb; color: white; border: 0; border-radius: 4px; cursor: pointer; }
section.results { padding: 16px 32px; }
section.results header.summary { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; }
section.results header.summary .actions a { font-size: 12px; color: #2563eb; text-decoration: none; margin-left: 12px; }
table { width: 100%; border-collapse: collapse; background: white; font-family: ui-monospace, SFMono-Regular, monospace; font-size: 12px; }
th, td { padding: 8px 12px; text-align: left; border-bottom: 1px solid #e5e7eb; vertical-align: top; }
th { background: #f3f4f6; font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em; color: #6b7280; }
.tag { display: inline-block; padding: 1px 8px; border-radius: 3px; font-size: 11px; font-weight: 600; }
.tag.success { background: #d1fae5; color: #065f46; }
.tag.failure { background: #fee2e2; color: #991b1b; }
.tag.denied  { background: #fef3c7; color: #92400e; }
.meta { color: #6b7280; font-size: 11px; max-width: 320px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pager { display: flex; gap: 8px; padding: 16px 0; justify-content: center; }
.pager a, .pager span { padding: 4px 12px; border-radius: 4px; font-size: 12px; }
.pager a { background: #e5e7eb; color: #111; text-decoration: none; }
.pager span { background: white; color: #6b7280; }
.err { padding: 16px 32px; background: #fee2e2; color: #991b1b; font-family: ui-monospace, SFMono-Regular, monospace; }
</style></head><body>`)
	sb.WriteString(`<header><h1>Audit log</h1></header>`)

	// Filter form — methods=GET so refresh stays clean + bookmarkable.
	sb.WriteString(`<form class="filters" method="GET">`)
	writeFilterInput(&sb, "actor", f.Actor, "")
	writeFilterInput(&sb, "action", f.Action, "e.g. user.*")
	writeFilterSelect(&sb, "outcome", string(f.Outcome),
		[][2]string{{"", "any"}, {"success", "success"}, {"failure", "failure"}, {"denied", "denied"}})
	writeFilterInput(&sb, "target_type", f.TargetType, "")
	writeFilterInput(&sb, "target_id", f.TargetID, "")
	writeFilterInput(&sb, "from", timeQS(f.From), "RFC3339")
	writeFilterInput(&sb, "to", timeQS(f.To), "RFC3339")
	writeFilterInput(&sb, "limit", strconv.Itoa(f.Limit), "")
	sb.WriteString(`<button type="submit">Filter</button>`)
	sb.WriteString(`</form>`)

	if qerr != nil {
		fmt.Fprintf(&sb, `<div class="err">query error: %s</div>`, html.EscapeString(qerr.Error()))
	}

	sb.WriteString(`<section class="results">`)
	jsonURL := base + ".json?" + currentQS(c)
	fmt.Fprintf(&sb, `<header class="summary"><div><strong>%d</strong> events</div><div class="actions"><a href="%s">⇩ JSON export</a></div></header>`,
		len(events), html.EscapeString(jsonURL))
	sb.WriteString(`<table><thead><tr><th>Time</th><th>Actor</th><th>Action</th><th>Target</th><th>Outcome</th><th>Metadata</th></tr></thead><tbody>`)
	for _, e := range events {
		fmt.Fprintf(&sb, `<tr><td>%s</td><td>%s<br><span style="color:#9ca3af">%s</span></td><td>%s</td><td>%s %s</td><td>%s</td><td class="meta">%s</td></tr>`,
			html.EscapeString(e.OccurredAt.UTC().Format(time.RFC3339)),
			html.EscapeString(e.Actor.Subject),
			html.EscapeString(e.Actor.IP),
			html.EscapeString(e.Action),
			html.EscapeString(e.Target.Type),
			html.EscapeString(e.Target.ID),
			renderOutcome(string(e.Outcome)),
			html.EscapeString(metaPreview(e.Metadata)),
		)
	}
	sb.WriteString(`</tbody></table>`)

	// Paging.
	sb.WriteString(`<div class="pager">`)
	if f.Offset > 0 {
		prev := f
		prev.Offset = f.Offset - f.Limit
		if prev.Offset < 0 {
			prev.Offset = 0
		}
		fmt.Fprintf(&sb, `<a href="?%s">← prev</a>`, html.EscapeString(filterToQS(prev)))
	} else {
		sb.WriteString(`<span>← prev</span>`)
	}
	fmt.Fprintf(&sb, `<span>offset %d</span>`, f.Offset)
	if len(events) >= f.Limit {
		next := f
		next.Offset = f.Offset + f.Limit
		fmt.Fprintf(&sb, `<a href="?%s">next →</a>`, html.EscapeString(filterToQS(next)))
	} else {
		sb.WriteString(`<span>next →</span>`)
	}
	sb.WriteString(`</div></section></body></html>`)
	return []byte(sb.String())
}

func writeFilterInput(sb *strings.Builder, name, value, placeholder string) {
	fmt.Fprintf(sb, `<label>%s<input name="%s" value="%s" placeholder="%s"></label>`,
		html.EscapeString(name), html.EscapeString(name),
		html.EscapeString(value), html.EscapeString(placeholder))
}

func writeFilterSelect(sb *strings.Builder, name, value string, options [][2]string) {
	fmt.Fprintf(sb, `<label>%s<select name="%s">`, html.EscapeString(name), html.EscapeString(name))
	for _, opt := range options {
		sel := ""
		if opt[0] == value {
			sel = " selected"
		}
		fmt.Fprintf(sb, `<option value="%s"%s>%s</option>`,
			html.EscapeString(opt[0]), sel, html.EscapeString(opt[1]))
	}
	sb.WriteString(`</select></label>`)
}

func renderOutcome(o string) string {
	if o == "" {
		return ""
	}
	return fmt.Sprintf(`<span class="tag %s">%s</span>`, html.EscapeString(o), html.EscapeString(o))
}

func metaPreview(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	raw, _ := json.Marshal(m)
	return string(raw)
}

func timeQS(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func filterToQS(f audit.Filter) string {
	q := url.Values{}
	if f.Actor != "" {
		q.Set("actor", f.Actor)
	}
	if f.Action != "" {
		q.Set("action", f.Action)
	}
	if f.Outcome != "" {
		q.Set("outcome", string(f.Outcome))
	}
	if f.TargetType != "" {
		q.Set("target_type", f.TargetType)
	}
	if f.TargetID != "" {
		q.Set("target_id", f.TargetID)
	}
	if !f.From.IsZero() {
		q.Set("from", f.From.UTC().Format(time.RFC3339))
	}
	if !f.To.IsZero() {
		q.Set("to", f.To.UTC().Format(time.RFC3339))
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	if f.Offset > 0 {
		q.Set("offset", strconv.Itoa(f.Offset))
	}
	return q.Encode()
}

// currentQS returns the raw query-string of the inbound request so
// the "JSON export" link mirrors the operator's current filter.
func currentQS(c *fiber.Ctx) string {
	q := url.Values{}
	c.Context().QueryArgs().VisitAll(func(k, v []byte) {
		q.Set(string(k), string(v))
	})
	return q.Encode()
}
