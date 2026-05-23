package openapi_test

import (
	"strings"
	"testing"

	"github.com/theizzatbek/fibermap/openapi"
)

// uiCase describes one viewer constructor + the unpkg/jsdelivr asset
// fragment that distinguishes it from the others, so we can verify
// the right HTML body is returned.
type uiCase struct {
	name        string
	fn          func(specURL, title string) string
	wantMarker  string // appears in the returned HTML for THIS viewer only
	otherMarker string // appears in another viewer's HTML — must NOT be present here
}

func TestUI_Variants(t *testing.T) {
	cases := []uiCase{
		{
			name:        "SwaggerUI",
			fn:          openapi.SwaggerUI,
			wantMarker:  "swagger-ui-dist",
			otherMarker: "redoc.standalone",
		},
		{
			name:        "Redoc",
			fn:          openapi.Redoc,
			wantMarker:  "redoc.standalone",
			otherMarker: "swagger-ui-dist",
		},
		{
			name:        "Scalar",
			fn:          openapi.Scalar,
			wantMarker:  "scalar/api-reference",
			otherMarker: "swagger-ui-dist",
		},
	}

	const specURL = "/api/openapi.json"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			html := tc.fn(specURL, "Demo API")
			if !strings.Contains(html, tc.wantMarker) {
				t.Errorf("missing %q in output: %s", tc.wantMarker, html)
			}
			if strings.Contains(html, tc.otherMarker) {
				t.Errorf("unexpected %q in output (mixed viewers?)", tc.otherMarker)
			}
			if !strings.Contains(html, specURL) {
				t.Errorf("specURL %q not embedded in HTML", specURL)
			}
			if !strings.Contains(html, "<title>Demo API</title>") {
				t.Errorf("title not embedded; got: %s", html)
			}
		})
	}
}

func TestUI_EmptyTitle_UsesDefault(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func(string, string) string
	}{
		{"SwaggerUI", openapi.SwaggerUI},
		{"Redoc", openapi.Redoc},
		{"Scalar", openapi.Scalar},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.fn("/spec.json", "")
			if !strings.Contains(out, "<title>API Documentation</title>") {
				t.Errorf("default title missing in %s", tc.name)
			}
		})
	}
}

func TestUI_EscapesHostileInput(t *testing.T) {
	// User-controlled title/specURL must not break out of the HTML
	// context — html.EscapeString covers <, >, &, ", '.
	hostileTitle := `<script>alert(1)</script>`
	hostileURL := `"><script>alert(2)</script>`

	out := openapi.SwaggerUI(hostileURL, hostileTitle)
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Errorf("title was not escaped — XSS surface: %s", out)
	}
	if strings.Contains(out, `"><script>alert(2)</script>`) {
		t.Errorf("specURL was not escaped — XSS surface: %s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("escaped title missing: %s", out)
	}
}
