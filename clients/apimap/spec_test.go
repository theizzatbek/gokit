package apimap

import (
	"errors"
	"os"
	"strings"
	"testing"

	xerrs "github.com/theizzatbek/gokit/errs"
)

func loadRaw(t *testing.T, fixture string) *rawConfig {
	t.Helper()
	b, err := os.ReadFile("testdata/" + fixture)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := parseBytes(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestValidate_Minimal(t *testing.T) {
	cfg := loadRaw(t, "minimal.yaml")
	if err := cfg.validate(nil); err != nil {
		t.Errorf("validate minimal: %v", err)
	}
}

func TestValidate_MultiClient(t *testing.T) {
	cfg := loadRaw(t, "multi_client.yaml")
	if err := cfg.validate(nil); err != nil {
		t.Errorf("validate multi_client: %v", err)
	}
}

func TestValidate_InvalidFixtures(t *testing.T) {
	tests := []struct {
		fixture  string
		wantCode string
	}{
		{"invalid_duplicate_client.yaml", CodeDuplicateClient},
		{"invalid_duplicate_endpoint.yaml", CodeDuplicateEndpoint},
		{"invalid_base_url.yaml", CodeInvalidBaseURL},
		{"invalid_method.yaml", CodeInvalidMethod},
		{"invalid_encode.yaml", CodeInvalidEncode},
		{"invalid_decode.yaml", CodeInvalidDecode},
		{"invalid_path_var.yaml", CodeInvalidPathVar},
		{"invalid_missing_name.yaml", CodeMissingClientName},
		{"invalid_auth_type.yaml", CodeAuthInvalidType},
		{"invalid_auth_bearer_no_token.yaml", CodeAuthMissingField},
		{"invalid_auth_basic_no_pass.yaml", CodeAuthMissingField},
		{"invalid_auth_header_no_name.yaml", CodeAuthMissingField},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			cfg := loadRaw(t, tt.fixture)
			err := cfg.validate(nil)
			if err == nil {
				t.Fatalf("validate %s: nil error, want %s", tt.fixture, tt.wantCode)
			}
			if !containsCode(err, tt.wantCode) {
				t.Errorf("validate %s: err = %v, want code %s", tt.fixture, err, tt.wantCode)
			}
		})
	}
}

func TestValidate_RegisteredEndpointMissing(t *testing.T) {
	cfg := loadRaw(t, "minimal.yaml")
	regs := map[string]struct{}{"github.unknown": {}}
	err := cfg.validate(regs)
	if err == nil {
		t.Fatal("validate: nil error, want CodeRegisteredEndpointMissing")
	}
	if !containsCode(err, CodeRegisteredEndpointMissing) {
		t.Errorf("err = %v, want code %s", err, CodeRegisteredEndpointMissing)
	}
}

func TestValidate_AggregatesMultipleErrors(t *testing.T) {
	cfg := &rawConfig{
		Clients: []rawClient{
			{Name: "", BaseURL: "not-a-url", Endpoints: []rawEndpoint{
				{Name: "a", Method: "BREW", Path: "/{1bad}"},
			}},
		},
	}
	err := cfg.validate(nil)
	if err == nil {
		t.Fatal("nil error, want aggregated validation errors")
	}
	mentions := 0
	for _, code := range []string{
		CodeMissingClientName, CodeInvalidBaseURL,
		CodeInvalidMethod, CodeInvalidPathVar,
	} {
		if strings.Contains(err.Error(), code) {
			mentions++
		}
	}
	if mentions < 3 {
		t.Errorf("aggregated err = %v, want at least 3 of the codes mentioned", err)
	}
}

// containsCode returns true if err (a possibly errors.Join'd chain) contains
// a *xerrs.Error with the given Code anywhere in the tree.
func containsCode(err error, code string) bool {
	if err == nil {
		return false
	}
	var e *xerrs.Error
	if errors.As(err, &e) && e.Code == code {
		return true
	}
	type unwrapper interface{ Unwrap() []error }
	if u, ok := err.(unwrapper); ok {
		for _, sub := range u.Unwrap() {
			if containsCode(sub, code) {
				return true
			}
		}
	}
	return false
}
