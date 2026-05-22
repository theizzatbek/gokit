package fibermap

import (
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
	if len(post.Roles) != 1 || post.Roles[0] != "director" {
		t.Errorf("roles = %v", post.Roles)
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
	if got := cfg.MiddlewareSets["protected"]; len(got) != 3 || got[0] != "base" || got[1] != "auth" || got[2] != "authorized" {
		t.Errorf("protected set = %v, want [base auth authorized]", got)
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
	if fe.Code != CodeInvalidYAML {
		t.Errorf("code = %q", fe.Code)
	}
	if fe.File == "" {
		t.Errorf("File should be populated")
	}
}
