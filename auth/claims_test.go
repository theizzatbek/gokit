package auth

import (
	"encoding/json"
	"reflect"
	"testing"
)

type testClaims struct {
	TenantID string `json:"tenant_id,omitempty"`
	Plan     string `json:"plan,omitempty"`
}

func TestClaims_MarshalFlattensCustom(t *testing.T) {
	c := Claims[testClaims]{
		Issuer:    "myapp",
		Subject:   "u-1",
		Audience:  []string{"web"},
		ExpiresAt: 100,
		IssuedAt:  10,
		JTI:       "j-1",
		Scopes:    []string{"a", "b"},
		Roles:     []string{"admin"},
		Custom:    testClaims{TenantID: "t-9", Plan: "pro"},
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	for _, k := range []string{"iss", "sub", "aud", "exp", "iat", "jti", "scopes", "roles", "tenant_id", "plan"} {
		if _, ok := out[k]; !ok {
			t.Errorf("flat object missing key %q; got %v", k, out)
		}
	}
}

func TestClaims_UnmarshalPopulatesBothRegisteredAndCustom(t *testing.T) {
	raw := []byte(`{"iss":"myapp","sub":"u-1","aud":["web"],"exp":100,"iat":10,"jti":"j-1","scopes":["a"],"roles":["admin"],"tenant_id":"t-9","plan":"pro"}`)
	var c Claims[testClaims]
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := Claims[testClaims]{
		Issuer: "myapp", Subject: "u-1", Audience: []string{"web"},
		ExpiresAt: 100, IssuedAt: 10, JTI: "j-1",
		Scopes: []string{"a"}, Roles: []string{"admin"},
		Custom: testClaims{TenantID: "t-9", Plan: "pro"},
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("Claims = %#v\nwant         %#v", c, want)
	}
}

func TestClaims_MarshalRegisteredWinsOnCollision(t *testing.T) {
	// Custom field reuses a registered tag ("sub"). Spec: registered wins,
	// Custom value silently dropped on marshal.
	type colliding struct {
		Sub string `json:"sub"`
	}
	c := Claims[colliding]{Subject: "registered", Custom: colliding{Sub: "shadow"}}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out["sub"] != "registered" {
		t.Fatalf("sub = %v, want \"registered\"", out["sub"])
	}
}

func TestClaims_AudienceOmittedWhenEmpty(t *testing.T) {
	c := Claims[testClaims]{Subject: "u-1", ExpiresAt: 1, IssuedAt: 1}
	b, _ := json.Marshal(c)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if _, present := out["aud"]; present {
		t.Fatalf("aud should be omitted when empty; got %v", out)
	}
}
