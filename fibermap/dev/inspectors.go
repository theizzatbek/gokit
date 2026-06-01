package dev

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// RoutesHandler returns a Fiber handler that lists every route the
// supplied *fiber.App has mounted. Content-negotiates: HTML for
// browsers, JSON for everything else.
//
// Mount under a dev-only path:
//
//	app.Get("/_dev/routes", dev.RoutesHandler(app))
func RoutesHandler(app *fiber.App) fiber.Handler {
	return func(c *fiber.Ctx) error {
		type entry struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		}
		var rows []entry
		for _, stack := range app.Stack() {
			for _, route := range stack {
				rows = append(rows, entry{Method: route.Method, Path: route.Path})
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Path == rows[j].Path {
				return rows[i].Method < rows[j].Method
			}
			return rows[i].Path < rows[j].Path
		})
		if !strings.Contains(c.Get(fiber.HeaderAccept), "text/html") {
			return c.JSON(rows)
		}
		var sb strings.Builder
		sb.WriteString(devHeader("Routes"))
		sb.WriteString(`<table><thead><tr><th>METHOD</th><th>PATH</th></tr></thead><tbody>`)
		for _, r := range rows {
			fmt.Fprintf(&sb, `<tr><td><span class="method">%s</span></td><td>%s</td></tr>`,
				html.EscapeString(r.Method), html.EscapeString(r.Path))
		}
		sb.WriteString(`</tbody></table>`)
		sb.WriteString(devFooter())
		c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
		return c.Send([]byte(sb.String()))
	}
}

// ConfigHandler returns a Fiber handler that renders the effective
// process environment with kit's known-secret env vars redacted.
// Content-negotiates HTML vs JSON like [RoutesHandler].
//
// Redaction: any env var whose name matches a sensitive substring
// (PASSWORD, SECRET, TOKEN, KEY, DSN, URL — when it carries
// credentials) gets its value replaced with "***". Operator can
// extend via [WithExtraRedaction].
func ConfigHandler(opts ...ConfigOption) fiber.Handler {
	cfg := configHandlerCfg{}
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(c *fiber.Ctx) error {
		env := os.Environ()
		sort.Strings(env)
		rows := make([]envRow, 0, len(env))
		for _, kv := range env {
			i := strings.IndexByte(kv, '=')
			if i < 0 {
				continue
			}
			k, v := kv[:i], kv[i+1:]
			if isSensitive(k, cfg.extraRedaction) {
				v = "***"
			}
			rows = append(rows, envRow{Key: k, Value: v})
		}
		if !strings.Contains(c.Get(fiber.HeaderAccept), "text/html") {
			c.Set(fiber.HeaderContentType, "application/json")
			return json.NewEncoder(c).Encode(rows)
		}
		var sb strings.Builder
		sb.WriteString(devHeader("Config / Environment"))
		sb.WriteString(`<table><thead><tr><th>KEY</th><th>VALUE</th></tr></thead><tbody>`)
		for _, r := range rows {
			fmt.Fprintf(&sb, `<tr><td>%s</td><td>%s</td></tr>`,
				html.EscapeString(r.Key), html.EscapeString(r.Value))
		}
		sb.WriteString(`</tbody></table>`)
		sb.WriteString(devFooter())
		c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
		return c.Send([]byte(sb.String()))
	}
}

type envRow struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ConfigOption tunes [ConfigHandler].
type ConfigOption func(*configHandlerCfg)

type configHandlerCfg struct {
	extraRedaction []string
}

// WithExtraRedaction adds substrings that mark env-var names as
// sensitive (matched case-insensitively). The default list covers
// PASSWORD / SECRET / TOKEN / KEY / DSN / DATABASE_URL / REDIS_URL.
func WithExtraRedaction(substrings ...string) ConfigOption {
	return func(c *configHandlerCfg) {
		c.extraRedaction = append(c.extraRedaction, substrings...)
	}
}

var defaultSensitive = []string{
	"PASSWORD", "SECRET", "TOKEN", "PRIVATE", "KEY",
	"DSN", "DATABASE_URL", "REDIS_URL", "NATS_URL",
}

func isSensitive(key string, extra []string) bool {
	up := strings.ToUpper(key)
	for _, s := range defaultSensitive {
		if strings.Contains(up, s) {
			return true
		}
	}
	for _, s := range extra {
		if strings.Contains(up, strings.ToUpper(s)) {
			return true
		}
	}
	return false
}

func devHeader(title string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<title>` + html.EscapeString(title) + `</title>` +
		`<style>
body { font: 14px/1.5 -apple-system, BlinkMacSystemFont, sans-serif; margin: 0; color: #1a1a1a; }
header { background: #1e3a8a; color: white; padding: 18px 32px; font-size: 22px; font-weight: 600; }
section { padding: 16px 32px; }
table { border-collapse: collapse; width: 100%; font-family: ui-monospace, SFMono-Regular, monospace; font-size: 13px; }
th, td { text-align: left; padding: 6px 12px; border-bottom: 1px solid #e5e7eb; }
th { font-size: 11px; text-transform: uppercase; letter-spacing: 0.08em; color: #6b7280; }
.method { display: inline-block; padding: 1px 8px; border-radius: 3px; background: #eef2ff; color: #3730a3; font-weight: 600; }
footer { padding: 12px 32px; color: #9ca3af; font-size: 12px; }
</style></head><body>` +
		`<header>` + html.EscapeString(title) + `</header><section>`
}

func devFooter() string {
	return `</section><footer>gokit dev inspector — disable for production by setting ENV != dev.</footer></body></html>`
}
