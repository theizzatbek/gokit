package runbook

import (
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/audit"
)

// Mount registers HTTP handlers on app under base for ops admin:
//
//	GET   <base>          → HTML page listing every flag + toggle form
//	GET   <base>.json     → JSON snapshot of the cache
//	POST  <base>/:flag    → toggle the named flag; body {"enabled": true/false}
//
// Callers wire auth/role-check middleware in front of app.Use(base);
// the package deliberately ships no auth — different services have
// different role conventions.
//
// SubjectFn pulls the actor from the request; without it audit
// events have an empty Actor.Subject.
func Mount(app fiber.Router, base string, r *Runbook, opts ...MountOption) {
	cfg := mountConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	base = strings.TrimRight(base, "/")
	app.Get(base, htmlHandler(r))
	app.Get(base+".json", jsonHandler(r))
	app.Post(base+"/:flag", postHandler(r, cfg))
}

// MountOption tunes [Mount].
type MountOption func(*mountConfig)

type mountConfig struct {
	subjectFn func(*fiber.Ctx) string
}

// WithSubjectFn extracts the actor subject from the request for
// audit-event Actor.Subject. nil leaves it empty.
func WithSubjectFn(fn func(*fiber.Ctx) string) MountOption {
	return func(c *mountConfig) { c.subjectFn = fn }
}

func htmlHandler(r *Runbook) fiber.Handler {
	return func(c *fiber.Ctx) error {
		flags := r.All()
		c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
		return c.Send(renderHTML(flags))
	}
}

func jsonHandler(r *Runbook) fiber.Handler {
	return func(c *fiber.Ctx) error {
		return c.JSON(r.All())
	}
}

type postBody struct {
	Enabled bool `json:"enabled"`
}

func postHandler(r *Runbook, cfg mountConfig) fiber.Handler {
	return func(c *fiber.Ctx) error {
		flag := c.Params("flag")
		var body postBody
		if err := json.Unmarshal(c.Body(), &body); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(map[string]string{
				"error": "invalid body — expected {\"enabled\": true|false}",
			})
		}
		actor := audit.Actor{IP: c.IP(), UA: c.Get(fiber.HeaderUserAgent)}
		if cfg.subjectFn != nil {
			actor.Subject = cfg.subjectFn(c)
		}
		if err := r.SetEnabled(c.UserContext(), flag, body.Enabled, actor); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(map[string]string{
				"error": err.Error(),
			})
		}
		return c.JSON(map[string]any{
			"flag": flag, "enabled": body.Enabled,
		})
	}
}

func renderHTML(flags map[string]bool) []byte {
	keys := make([]string, 0, len(flags))
	for k := range flags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(`<!doctype html><html><head><meta charset="utf-8"><title>runbook</title><style>
body { font: 13px -apple-system, BlinkMacSystemFont, sans-serif; margin: 0; }
header { background: #7c2d12; color: white; padding: 16px 32px; font-size: 18px; font-weight: 600; }
table { width: 100%; border-collapse: collapse; margin: 0; }
th, td { padding: 8px 16px; border-bottom: 1px solid #eee; font-family: ui-monospace, SFMono-Regular, monospace; }
th { background: #f5f5f5; font-size: 11px; letter-spacing: 0.06em; text-transform: uppercase; color: #6b7280; }
button { font: 12px sans-serif; padding: 4px 14px; border: 0; border-radius: 4px; cursor: pointer; }
.on { background: #d1fae5; color: #065f46; }
.off { background: #fee2e2; color: #991b1b; }
section { padding: 20px 32px; color: #6b7280; font-size: 12px; }
</style></head><body>`)
	sb.WriteString(`<header>Runbook — runtime feature flags</header>`)
	sb.WriteString(`<table><thead><tr><th>FLAG</th><th>STATE</th><th>TOGGLE</th></tr></thead><tbody>`)
	for _, k := range keys {
		v := flags[k]
		state := "ENABLED"
		cls := "on"
		nextOp := "false"
		if !v {
			state = "DISABLED"
			cls = "off"
			nextOp = "true"
		}
		fmt.Fprintf(&sb, `<tr><td>%s</td><td><span class="%s" style="padding:1px 8px;border-radius:3px;font-weight:600;">%s</span></td>`,
			html.EscapeString(k), cls, state)
		fmt.Fprintf(&sb, `<td><button class="%s" onclick="toggle('%s',%s)">flip to %s</button></td></tr>`,
			cls, html.EscapeString(k), nextOp, strings.ToUpper(nextOp))
	}
	sb.WriteString(`</tbody></table>`)
	sb.WriteString(`<section>POST <code>{base}/{flag}</code> with body <code>{"enabled":true|false}</code> from your terminal to toggle. Default-on: flags without an explicit value are enabled.</section>`)
	sb.WriteString(`<script>
function toggle(flag, enabled){
  fetch(location.pathname + "/" + flag, {
    method: "POST", headers: {"Content-Type":"application/json"},
    body: JSON.stringify({enabled})
  }).then(r => location.reload());
}
</script></body></html>`)
	return []byte(sb.String())
}
