package fibermap

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBytes_Basic(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "basic.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := parseBytes(data, "basic.yaml")
	if err != nil {
		t.Fatalf("parseBytes: %v", err)
	}

	if len(cfg.Groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(cfg.Groups))
	}
	g := cfg.Groups[0]
	if g.Prefix != "/v1" {
		t.Errorf("prefix = %q", g.Prefix)
	}
	if len(g.Routes) != 1 {
		t.Fatalf("want 1 route, got %d", len(g.Routes))
	}
	r := g.Routes[0]
	if r.Method != "GET" || r.Path != "/ping" || r.Handler != "ping.handle" {
		t.Errorf("route mismatch: %+v", r)
	}
}

func TestParseBytes_NestedGroups(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "nested_groups.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := parseBytes(data, "nested_groups.yaml")
	if err != nil {
		t.Fatal(err)
	}

	v1 := cfg.Groups[0]
	if len(v1.Groups) != 1 || v1.Groups[0].Prefix != "/patients" {
		t.Fatalf("unexpected nested groups: %+v", v1.Groups)
	}
	if len(v1.Groups[0].Routes) != 2 {
		t.Fatalf("want 2 routes, got %d", len(v1.Groups[0].Routes))
	}
	post := v1.Groups[0].Routes[1]
	if len(post.Middleware) != 1 || post.Middleware[0].Name != "require_role" ||
		len(post.Middleware[0].Args) != 1 || post.Middleware[0].Args[0] != "director" {
		t.Errorf("middleware = %+v", post.Middleware)
	}
}

func TestParseBytes_MissingMethod(t *testing.T) {
	data := []byte(`
groups:
  - prefix: /v1
    routes:
      - { path: /x, handler: x.handle }
`)
	_, err := parseBytes(data, "")

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if fe.Code != CodeMissingField {
		t.Errorf("code = %q, want %q", fe.Code, CodeMissingField)
	}
	if !strings.HasSuffix(fe.Path, ".method") {
		t.Errorf("Path = %q, want suffix .method", fe.Path)
	}
	// Route flow-mapping is on source line 5 (line 1 = blank from \n,
	// line 5 = the `- { ... }` item).
	if fe.Line != 5 {
		t.Errorf("Line = %d, want 5", fe.Line)
	}
}

func TestParseBytes_InvalidMethod(t *testing.T) {
	data := []byte(`
groups:
  - prefix: /v1
    routes:
      - { method: FLY, path: /x, handler: x.handle }
`)
	_, err := parseBytes(data, "")

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if fe.Code != CodeInvalidHTTPMethod {
		t.Errorf("code = %q, want %q", fe.Code, CodeInvalidHTTPMethod)
	}
	if !strings.HasSuffix(fe.Path, ".method") {
		t.Errorf("Path = %q, want suffix .method", fe.Path)
	}
	if fe.Line != 5 {
		t.Errorf("Line = %d, want 5", fe.Line)
	}
}

func TestParseBytes_MiddlewareSetCycle(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "invalid_cycle.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = parseBytes(data, "invalid_cycle.yaml")

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if fe.Code != CodeMiddlewareCycle {
		t.Errorf("code = %q, want %q", fe.Code, CodeMiddlewareCycle)
	}
	if !strings.Contains(fe.Message, "a") || !strings.Contains(fe.Message, "b") {
		t.Errorf("message should mention both nodes: %q", fe.Message)
	}
}

func TestParseBytes_MalformedYAML(t *testing.T) {
	_, err := parseBytes([]byte("not: : yaml"), "")

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if fe.Code != CodeInvalidYAML {
		t.Errorf("code = %q, want %q", fe.Code, CodeInvalidYAML)
	}
}

func TestParseBytes_MiddlewareSets(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "middleware_sets.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := parseBytes(data, "middleware_sets.yaml")
	if err != nil {
		t.Fatalf("parseBytes: %v", err)
	}

	if len(cfg.MiddlewareSets) != 2 {
		t.Errorf("middleware_sets count = %d, want 2", len(cfg.MiddlewareSets))
	}
	got := cfg.MiddlewareSets["protected"]
	want := []string{"base", "auth", "authorized"}
	if len(got) != len(want) {
		t.Fatalf("protected set len = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Name != w || len(got[i].Args) != 0 {
			t.Errorf("protected[%d] = %+v, want %s", i, got[i], w)
		}
	}
}

func TestParseBytes_FactoryMiddleware(t *testing.T) {
	data := []byte(`
groups:
  - prefix: /v1
    routes:
      - method: POST
        path: /x
        handler: x.create
        middleware:
          - audit
          - require_role: [admin, director]
`)
	cfg, err := parseBytes(data, "")
	if err != nil {
		t.Fatalf("parseBytes: %v", err)
	}
	mw := cfg.Groups[0].Routes[0].Middleware
	if len(mw) != 2 {
		t.Fatalf("middleware len = %d, want 2", len(mw))
	}
	if mw[0].Name != "audit" || len(mw[0].Args) != 0 {
		t.Errorf("mw[0] = %+v", mw[0])
	}
	if mw[1].Name != "require_role" || len(mw[1].Args) != 2 || mw[1].Args[0] != "admin" || mw[1].Args[1] != "director" {
		t.Errorf("mw[1] = %+v", mw[1])
	}
}

func TestParseBytes_FactoryMiddleware_BadShape(t *testing.T) {
	cases := []string{
		// two keys in one entry
		`
groups:
  - prefix: /v1
    routes:
      - method: GET
        path: /x
        handler: h
        middleware:
          - { a: [1], b: [2] }
`,
		// non-list value
		`
groups:
  - prefix: /v1
    routes:
      - method: GET
        path: /x
        handler: h
        middleware:
          - require_role: admin
`,
	}
	for i, src := range cases {
		_, err := parseBytes([]byte(src), "")
		var fe *Error
		if !errors.As(err, &fe) || fe.Code != CodeInvalidYAML {
			t.Errorf("case %d: want CodeInvalidYAML, got %v", i, err)
		}
	}
}

func TestLoadFileToConfig_Success(t *testing.T) {
	cfg, err := loadFileToConfig(filepath.Join("testdata", "basic.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Groups) != 1 {
		t.Errorf("groups = %d, want 1", len(cfg.Groups))
	}
}

func TestLoadFileToConfig_FileNotFound(t *testing.T) {
	_, err := loadFileToConfig(filepath.Join("testdata", "nope.yaml"))

	var fe *Error
	if !errors.As(err, &fe) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if fe.Code != CodeFileNotFound {
		t.Errorf("code = %q, want %q", fe.Code, CodeFileNotFound)
	}
	if fe.File == "" {
		t.Errorf("File should be populated")
	}
}

func TestLint_OK(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "basic.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Lint(data); err != nil {
		t.Errorf("Lint on basic.yaml: %v", err)
	}
}

func TestLint_BadMethod(t *testing.T) {
	err := Lint([]byte(`
groups:
  - routes:
      - { method: FLY, path: /x, handler: x }
`))
	var fe *Error
	if !errors.As(err, &fe) || fe.Code != CodeInvalidHTTPMethod {
		t.Errorf("want CodeInvalidHTTPMethod, got %v", err)
	}
}

func TestLintFile_NotFound(t *testing.T) {
	err := LintFile(filepath.Join("testdata", "nope.yaml"))
	var fe *Error
	if !errors.As(err, &fe) || fe.Code != CodeFileNotFound {
		t.Errorf("want CodeFileNotFound, got %v", err)
	}
}

func TestSchema_IsValidJSON(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(Schema(), &m); err != nil {
		t.Fatalf("Schema() is not valid JSON: %v", err)
	}
	if m["$schema"] == nil {
		t.Errorf("schema missing $schema field")
	}
	if m["definitions"] == nil {
		t.Errorf("schema missing definitions")
	}
}
