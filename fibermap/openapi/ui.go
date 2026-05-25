package openapi

import (
	"html"
	"strings"
)

// SwaggerUI returns a self-contained HTML page that renders the
// OpenAPI document served at `specURL` using
// [Swagger UI](https://github.com/swagger-api/swagger-ui), loaded
// from the unpkg CDN. `title` becomes the `<title>` tag — pass empty
// for the default "API Documentation".
//
// Serve it via Engine.Add:
//
//	docs := openapi.SwaggerUI("/openapi.json", "Tasks API")
//	eng.Add("GET", "/docs", "openapi.docs",
//	    func(c *fibermap.Context[AppCtx]) error {
//	        c.Set("Content-Type", "text/html; charset=utf-8")
//	        return c.SendString(docs)
//	    })
//
// The returned string is static — generate once at startup, store in
// a variable, serve as bytes on every request. First page load
// fetches the Swagger UI bundle from unpkg.com; subsequent loads use
// the browser cache.
func SwaggerUI(specURL, title string) string {
	if title == "" {
		title = "API Documentation"
	}
	t := html.EscapeString(title)
	u := html.EscapeString(specURL)
	const tmpl = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{TITLE}}</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css" />
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js" crossorigin></script>
  <script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-standalone-preset.js" crossorigin></script>
  <script>
    window.onload = () => {
      window.ui = SwaggerUIBundle({
        url: "{{URL}}",
        dom_id: "#swagger-ui",
        deepLinking: true,
        presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
        layout: "StandaloneLayout",
      });
    };
  </script>
</body>
</html>
`
	out := strings.ReplaceAll(tmpl, "{{TITLE}}", t)
	out = strings.ReplaceAll(out, "{{URL}}", u)
	return out
}

// Redoc returns a self-contained HTML page that renders the OpenAPI
// document served at `specURL` using
// [Redoc](https://github.com/Redocly/redoc), loaded from the unpkg
// CDN. `title` becomes the `<title>` — empty for "API Documentation".
//
// Redoc is read-only (no "try it out" requester) but renders prettier
// and is friendlier to long specs. Pair with SwaggerUI when you want
// both — Redoc at `/docs`, Swagger UI at `/docs/explorer`, both
// pointing at the same `/openapi.json`.
func Redoc(specURL, title string) string {
	if title == "" {
		title = "API Documentation"
	}
	t := html.EscapeString(title)
	u := html.EscapeString(specURL)
	const tmpl = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{TITLE}}</title>
  <style>body { margin: 0; }</style>
</head>
<body>
  <redoc spec-url="{{URL}}"></redoc>
  <script src="https://unpkg.com/redoc@2.4.0/bundles/redoc.standalone.js" crossorigin></script>
</body>
</html>
`
	out := strings.ReplaceAll(tmpl, "{{TITLE}}", t)
	out = strings.ReplaceAll(out, "{{URL}}", u)
	return out
}

// Scalar returns a self-contained HTML page that renders the OpenAPI
// document served at `specURL` using
// [Scalar API Reference](https://github.com/scalar/scalar), loaded
// from the jsdelivr CDN. `title` becomes the `<title>` — empty for
// "API Documentation".
//
// Scalar is the modern option of the three: fast, dark-mode by
// default, with a built-in "try it out" client that reads
// authentication from the spec's security schemes.
func Scalar(specURL, title string) string {
	if title == "" {
		title = "API Documentation"
	}
	t := html.EscapeString(title)
	u := html.EscapeString(specURL)
	const tmpl = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>{{TITLE}}</title>
</head>
<body>
  <script id="api-reference" data-url="{{URL}}"></script>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body>
</html>
`
	out := strings.ReplaceAll(tmpl, "{{TITLE}}", t)
	out = strings.ReplaceAll(out, "{{URL}}", u)
	return out
}
