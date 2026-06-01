package dev

import (
	"errors"
	"fmt"
	"html"
	"runtime"
	"strings"

	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
)

// ErrorHandler wraps inner so requests with `Accept: text/html` get
// a developer-friendly HTML error page instead of the kit's JSON
// error shape. Non-HTML clients (`Accept: application/json`, default)
// fall through to inner unchanged.
//
// The HTML page shows:
//   - Method + path + status code + error message
//   - Captured stack frame (file:line) for the calling site
//   - Pretty-printed request headers
//   - Error code + kind when the error is *errs.Error
//
// Pass nil to wrap [fibermap.ErrorHandler(nil)] (the kit's default).
func ErrorHandler(inner fiber.ErrorHandler) fiber.ErrorHandler {
	if inner == nil {
		inner = fibermap.ErrorHandler(nil)
	}
	return func(c *fiber.Ctx, err error) error {
		accept := c.Get(fiber.HeaderAccept)
		// Defer to inner when client doesn't want HTML — APIs,
		// curl without -H, fetch() in production code.
		if !strings.Contains(accept, "text/html") {
			return inner(c, err)
		}
		status, code, kind, msg := classifyError(err)
		c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
		return c.Status(status).Send(renderErrorHTML(c, status, code, kind, msg, err))
	}
}

func classifyError(err error) (status int, code, kind, msg string) {
	var ke *xerrs.Error
	if errors.As(err, &ke) {
		s, _ := xerrs.HTTP(ke)
		return s, ke.Code, ke.Kind.String(), ke.Message
	}
	var fe *fiber.Error
	if errors.As(err, &fe) {
		return fe.Code, "", "", fe.Message
	}
	return fiber.StatusInternalServerError, "", "", err.Error()
}

func renderErrorHTML(c *fiber.Ctx, status int, code, kind, msg string, raw error) []byte {
	var sb strings.Builder
	sb.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	fmt.Fprintf(&sb, `<title>%d %s</title>`, status, html.EscapeString(msg))
	sb.WriteString(`<style>
body { font: 14px/1.5 -apple-system, BlinkMacSystemFont, sans-serif; margin: 0; color: #1a1a1a; }
header { background: #b91c1c; color: white; padding: 24px 32px; }
header h1 { margin: 0; font-size: 22px; }
header .status { font-size: 56px; font-weight: 700; line-height: 1; }
header .msg { margin-top: 8px; opacity: 0.92; }
section { padding: 20px 32px; border-bottom: 1px solid #e5e5e5; }
section h2 { margin: 0 0 12px; font-size: 13px; text-transform: uppercase; letter-spacing: 0.08em; color: #6b7280; }
dl { display: grid; grid-template-columns: auto 1fr; gap: 6px 16px; margin: 0; }
dt { font-weight: 600; color: #374151; }
dd { margin: 0; font-family: ui-monospace, SFMono-Regular, monospace; color: #111; }
pre { background: #f3f4f6; padding: 12px; border-radius: 6px; overflow: auto; font-family: ui-monospace, SFMono-Regular, monospace; font-size: 12px; line-height: 1.45; margin: 0; }
.tag { display: inline-block; padding: 2px 8px; border-radius: 4px; background: #fef3c7; color: #92400e; font-size: 12px; font-weight: 600; margin-right: 6px; }
footer { padding: 14px 32px; color: #9ca3af; font-size: 12px; }
</style></head><body>`)

	fmt.Fprintf(&sb, `<header><div class="status">%d</div><h1>%s %s</h1><div class="msg">%s</div></header>`,
		status, html.EscapeString(c.Method()), html.EscapeString(c.OriginalURL()),
		html.EscapeString(msg))

	if code != "" || kind != "" {
		sb.WriteString(`<section><h2>Error</h2>`)
		if code != "" {
			fmt.Fprintf(&sb, `<span class="tag">code</span>%s `, html.EscapeString(code))
		}
		if kind != "" {
			fmt.Fprintf(&sb, `<span class="tag">kind</span>%s`, html.EscapeString(kind))
		}
		sb.WriteString(`</section>`)
	}

	sb.WriteString(`<section><h2>Request</h2><dl>`)
	fmt.Fprintf(&sb, `<dt>Method</dt><dd>%s</dd>`, html.EscapeString(c.Method()))
	fmt.Fprintf(&sb, `<dt>Path</dt><dd>%s</dd>`, html.EscapeString(c.Path()))
	fmt.Fprintf(&sb, `<dt>Route</dt><dd>%s</dd>`, html.EscapeString(c.Route().Path))
	fmt.Fprintf(&sb, `<dt>IP</dt><dd>%s</dd>`, html.EscapeString(c.IP()))
	sb.WriteString(`</dl></section>`)

	sb.WriteString(`<section><h2>Headers</h2><pre>`)
	c.Request().Header.VisitAll(func(k, v []byte) {
		fmt.Fprintf(&sb, "%s: %s\n", html.EscapeString(string(k)), html.EscapeString(string(v)))
	})
	sb.WriteString(`</pre></section>`)

	sb.WriteString(`<section><h2>Stack</h2><pre>`)
	sb.WriteString(html.EscapeString(captureStack()))
	sb.WriteString(`</pre></section>`)

	if raw != nil {
		sb.WriteString(`<section><h2>Raw error</h2><pre>`)
		sb.WriteString(html.EscapeString(fmt.Sprintf("%+v", raw)))
		sb.WriteString(`</pre></section>`)
	}

	sb.WriteString(`<footer>gokit dev error page — visible only when ENV=dev. Switch your client to `)
	sb.WriteString(`<code>Accept: application/json</code> for the production error shape.</footer>`)
	sb.WriteString(`</body></html>`)
	return []byte(sb.String())
}

// captureStack returns a trimmed runtime stack — anything above the
// kit's error-handler frames (which clutter the dev view).
func captureStack() string {
	buf := make([]byte, 8192)
	n := runtime.Stack(buf, false)
	return string(buf[:n])
}
