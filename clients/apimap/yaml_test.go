package apimap

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	xerrs "github.com/theizzatbek/fibermap/errs"
)

func TestParseBytes_Minimal(t *testing.T) {
	b, err := os.ReadFile("testdata/minimal.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := parseBytes(b)
	if err != nil {
		t.Fatalf("parseBytes: %v", err)
	}
	if len(cfg.Clients) != 1 {
		t.Fatalf("len(Clients) = %d, want 1", len(cfg.Clients))
	}
	c := cfg.Clients[0]
	if c.Name != "github" {
		t.Errorf("Name = %q, want github", c.Name)
	}
	if c.BaseURL != "https://api.github.com" {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
	if c.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s", c.Timeout)
	}
	if c.DefaultHeaders["Accept"] != "application/vnd.github+json" {
		t.Errorf("missing Accept header: %v", c.DefaultHeaders)
	}
	if len(c.Endpoints) != 1 || c.Endpoints[0].Name != "get_user" {
		t.Errorf("endpoints = %+v", c.Endpoints)
	}
	if c.Endpoints[0].Decode != "json" {
		t.Errorf("Decode = %q, want json", c.Endpoints[0].Decode)
	}
}

func TestParseBytes_MultiClient(t *testing.T) {
	b, err := os.ReadFile("testdata/multi_client.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := parseBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Clients) != 2 {
		t.Fatalf("len(Clients) = %d, want 2", len(cfg.Clients))
	}
	names := []string{cfg.Clients[0].Name, cfg.Clients[1].Name}
	if names[0] != "github" || names[1] != "stripe" {
		t.Errorf("client names = %v, want [github stripe]", names)
	}
}

func TestParseBytes_SyntaxError(t *testing.T) {
	bad := []byte("clients: [\n  - name: github\n    base_url:\n  invalid_indent")
	_, err := parseBytes(bad)
	if err == nil {
		t.Fatal("parseBytes succeeded on malformed YAML, want error")
	}
	if !strings.Contains(err.Error(), "line") && !strings.Contains(err.Error(), "yaml") {
		t.Errorf("err = %v, want yaml-flavoured error", err)
	}
}

func TestParseBytes_EmptyClientsRejected(t *testing.T) {
	_, err := parseBytes([]byte("clients: []\n"))
	if err == nil {
		t.Fatal("parseBytes on empty clients: succeeded, want CodeNoClients")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeNoClients {
		t.Errorf("err = %v, want *xerrs.Error{Code: %q}", err, CodeNoClients)
	}
}

func TestParseBytes_EnvSubst_Happy(t *testing.T) {
	t.Setenv("GH_TOK", "ghp_abc123")
	t.Setenv("GH_USER", "torvalds")
	yaml := []byte(`clients:
  - name: ${GH_USER}
    base_url: https://api.github.com
    auth:
      type: bearer
      token: ${GH_TOK}
    endpoints: [{name: a, method: GET, path: /a}]
`)
	cfg, err := parseBytes(yaml)
	if err != nil {
		t.Fatalf("parseBytes: %v", err)
	}
	if cfg.Clients[0].Name != "torvalds" {
		t.Errorf("Name = %q, want torvalds", cfg.Clients[0].Name)
	}
	if cfg.Clients[0].Auth == nil || cfg.Clients[0].Auth.Token != "ghp_abc123" {
		t.Errorf("token not substituted: %+v", cfg.Clients[0].Auth)
	}
}

func TestParseBytes_EnvSubst_MissingVarRejected(t *testing.T) {
	os.Unsetenv("MUST_NOT_BE_SET_FOR_TEST")
	yaml := []byte(`clients:
  - name: c1
    base_url: ${MUST_NOT_BE_SET_FOR_TEST}
    endpoints: [{name: a, method: GET, path: /a}]
`)
	_, err := parseBytes(yaml)
	if err == nil {
		t.Fatal("parseBytes succeeded, want CodeEnvVarUnset")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeEnvVarUnset {
		t.Errorf("err = %v, want code %s", err, CodeEnvVarUnset)
	}
}

func TestParseBytes_EnvSubst_MalformedRejected(t *testing.T) {
	yaml := []byte(`clients:
  - name: ${lowercase_not_allowed}
    base_url: https://x
    endpoints: [{name: a, method: GET, path: /a}]
`)
	_, err := parseBytes(yaml)
	if err == nil {
		t.Fatal("parseBytes succeeded, want CodeEnvVarMalformed")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeEnvVarMalformed {
		t.Errorf("err = %v, want code %s", err, CodeEnvVarMalformed)
	}
}

func TestParseBytes_EnvSubst_LiteralDollarPassesThrough(t *testing.T) {
	yaml := []byte(`clients:
  - name: c1
    base_url: https://api.example.com/v$50
    endpoints: [{name: a, method: GET, path: /a}]
`)
	cfg, err := parseBytes(yaml)
	if err != nil {
		t.Fatalf("parseBytes: %v", err)
	}
	if cfg.Clients[0].BaseURL != "https://api.example.com/v$50" {
		t.Errorf("BaseURL = %q, want literal $50 preserved", cfg.Clients[0].BaseURL)
	}
}

func TestParsePathTemplate(t *testing.T) {
	tests := []struct {
		path     string
		wantVars []string
		wantErr  string
	}{
		{"/users/{username}", []string{"username"}, ""},
		{"/", nil, ""},
		{"/static", nil, ""},
		{"/a/{x}/b/{y}/c", []string{"x", "y"}, ""},
		{"/path/{Var_1}/x", []string{"Var_1"}, ""},
		{"/bad/{}", nil, CodeInvalidPathVar},
		{"/bad/{1abc}", nil, CodeInvalidPathVar},
		{"/bad/{a b}", nil, CodeInvalidPathVar},
		{"/dup/{x}/y/{x}", nil, CodeInvalidPathVar},
		{"/unmatched/{x", nil, CodeInvalidPathVar},
		{"/unmatched/x}", nil, CodeInvalidPathVar},
	}
	for _, tt := range tests {
		vars, err := parsePathTemplate(tt.path)
		if tt.wantErr == "" {
			if err != nil {
				t.Errorf("parsePathTemplate(%q) error = %v, want nil", tt.path, err)
				continue
			}
			if !equalStrings(vars, tt.wantVars) {
				t.Errorf("parsePathTemplate(%q) vars = %v, want %v", tt.path, vars, tt.wantVars)
			}
			continue
		}
		if err == nil {
			t.Errorf("parsePathTemplate(%q): nil error, want code %q", tt.path, tt.wantErr)
			continue
		}
		var e *xerrs.Error
		if !errors.As(err, &e) || e.Code != tt.wantErr {
			t.Errorf("parsePathTemplate(%q) = %v, want code %q", tt.path, err, tt.wantErr)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
