package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOpenAPIBlock_Present(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "routes.yaml")
	body := []byte(`groups: []
openapi:
  info:
    title: Test API
    version: 1.2.3
    description: hello
    contact:
      name: dev
      email: dev@example.com
  servers:
    - url: https://api.example.com
      description: prod
  security_schemes:
    BearerAuth:
      type: http
      scheme: bearer
      bearer_format: JWT
  middleware_security:
    auth: [BearerAuth]
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	y, err := parseOpenAPIBlock(path)
	if err != nil {
		t.Fatalf("parseOpenAPIBlock: %v", err)
	}
	if y == nil {
		t.Fatal("expected non-nil openapiYAML")
	}
	if y.Info == nil || y.Info.Title != "Test API" {
		t.Fatalf("Info: %+v", y.Info)
	}
	if y.Info.Contact == nil || y.Info.Contact.Email != "dev@example.com" {
		t.Fatalf("Contact: %+v", y.Info.Contact)
	}
	if len(y.Servers) != 1 || y.Servers[0].URL != "https://api.example.com" {
		t.Fatalf("Servers: %+v", y.Servers)
	}
	scheme, ok := y.SecuritySchemes["BearerAuth"]
	if !ok || scheme.Type != "http" || scheme.Scheme != "bearer" || scheme.BearerFormat != "JWT" {
		t.Fatalf("BearerAuth: %+v", scheme)
	}
	if got := y.MiddlewareSecurity["auth"]; len(got) != 1 || got[0] != "BearerAuth" {
		t.Fatalf("middleware_security[auth]: %v", got)
	}
}

func TestParseOpenAPIBlock_Absent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "routes.yaml")
	if err := os.WriteFile(path, []byte(`groups: []`+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	y, err := parseOpenAPIBlock(path)
	if err != nil {
		t.Fatalf("parseOpenAPIBlock: %v", err)
	}
	if y != nil {
		t.Fatalf("expected nil openapiYAML for absent block, got %+v", y)
	}
}

func TestParseOpenAPIBlock_Malformed(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "routes.yaml")
	body := []byte(`openapi:
  info:
    title: [this is a list not a string]
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := parseOpenAPIBlock(path)
	if err == nil || !strings.Contains(err.Error(), CodeOpenAPIYAMLParse) {
		t.Fatalf("want CodeOpenAPIYAMLParse, got %v", err)
	}
}

func TestToOpenAPIOptions_FullBlock(t *testing.T) {
	y := &openapiYAML{
		Info: &openapiInfoYAML{
			Title:       "T",
			Version:     "1",
			Description: "d",
		},
		Servers: []openapiServerYAML{{URL: "u", Description: "p"}},
		SecuritySchemes: map[string]openapiSchemeYAML{
			"B": {Type: "http", Scheme: "bearer"},
		},
		MiddlewareSecurity: map[string][]string{"auth": {"B"}},
	}
	opts := y.toOpenAPIOptions()
	// 1 WithInfo + 1 WithServer + 1 WithSecurity + 1 MapMiddlewareToSecurity
	if len(opts) != 4 {
		t.Fatalf("opts count: got %d want 4", len(opts))
	}
}
