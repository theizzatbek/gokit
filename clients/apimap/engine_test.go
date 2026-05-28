package apimap

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	xerrs "github.com/theizzatbek/gokit/errs"
)

type stubReq struct{ N int }
type stubResp struct{ S string }

func TestEngine_LoadBytes_Minimal(t *testing.T) {
	e := New()
	yaml := []byte(`clients:
  - name: c1
    base_url: https://example.com
    endpoints:
      - {name: a, method: GET, path: /a}
`)
	if err := e.LoadBytes(yaml); err != nil {
		t.Fatal(err)
	}
	if len(e.clients) != 1 || e.clients[0].Name != "c1" {
		t.Errorf("clients = %+v", e.clients)
	}
}

func TestEngine_LoadFile_Minimal(t *testing.T) {
	e := New()
	if err := e.LoadFile("testdata/minimal.yaml"); err != nil {
		t.Fatal(err)
	}
	if len(e.clients) != 1 {
		t.Errorf("clients = %d, want 1", len(e.clients))
	}
}

func TestEngine_LoadMulti_AppendsClients(t *testing.T) {
	e := New()
	if err := e.LoadFile("testdata/minimal.yaml"); err != nil {
		t.Fatal(err)
	}
	if err := e.LoadFile("testdata/multi_client.yaml"); err != nil {
		t.Fatal(err)
	}
	if len(e.clients) != 3 {
		t.Errorf("clients = %d, want 3 (1 + 2)", len(e.clients))
	}
}

func TestEngine_RegisterRequest_StoresType(t *testing.T) {
	e := New()
	RegisterRequest[stubReq](e, "c1.create")
	if _, ok := e.reqTypes["c1.create"]; !ok {
		t.Error("request type not stored")
	}
}

func TestEngine_RegisterResponse_StoresType(t *testing.T) {
	e := New()
	RegisterResponse[stubResp](e, "c1.fetch")
	if _, ok := e.respTypes["c1.fetch"]; !ok {
		t.Error("response type not stored")
	}
}

func TestEngine_DuplicateRegisterPanics(t *testing.T) {
	e := New()
	RegisterResponse[stubResp](e, "c1.fetch")
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("recovered non-error %v", r)
		}
		var xe *xerrs.Error
		if !errors.As(err, &xe) || xe.Code != CodeDuplicateEndpoint {
			t.Errorf("err = %v, want code %s", err, CodeDuplicateEndpoint)
		}
	}()
	RegisterResponse[stubResp](e, "c1.fetch")
}

func TestEngine_RegisterAfterBuiltPanics(t *testing.T) {
	e := New()
	e.built = true // simulate post-build state
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on register after Build")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("recovered non-error %v", r)
		}
		var xe *xerrs.Error
		if !errors.As(err, &xe) || xe.Code != CodeAlreadyBuilt {
			t.Errorf("err = %v, want code %s", err, CodeAlreadyBuilt)
		}
	}()
	RegisterResponse[stubResp](e, "c1.fetch")
}

func TestEngine_Build_Happy(t *testing.T) {
	e := New()
	if err := e.LoadFile("testdata/minimal.yaml"); err != nil {
		t.Fatal(err)
	}
	c, err := e.Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.endpoints["github.get_user"]; !ok {
		t.Errorf("missing endpoint github.get_user; have %v", c.endpoints)
	}
}

func TestEngine_Build_Twice_Rejected(t *testing.T) {
	e := New()
	if err := e.LoadFile("testdata/minimal.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Build(); err != nil {
		t.Fatal(err)
	}
	_, err := e.Build()
	if err == nil {
		t.Fatal("nil error, want CodeAlreadyBuilt")
	}
	var ee *xerrs.Error
	if !errors.As(err, &ee) || ee.Code != CodeAlreadyBuilt {
		t.Errorf("err = %v, want code %s", err, CodeAlreadyBuilt)
	}
}

func TestEngine_Build_PropagatesValidation(t *testing.T) {
	e := New()
	if err := e.LoadFile("testdata/invalid_method.yaml"); err != nil {
		t.Fatal(err)
	}
	_, err := e.Build()
	if err == nil || !containsCode(err, CodeInvalidMethod) {
		t.Errorf("err = %v, want code %s", err, CodeInvalidMethod)
	}
}

func TestEngine_Build_RegistrationsCrossChecked(t *testing.T) {
	e := New()
	if err := e.LoadFile("testdata/minimal.yaml"); err != nil {
		t.Fatal(err)
	}
	RegisterResponse[stubResp](e, "github.nope")
	_, err := e.Build()
	if err == nil || !containsCode(err, CodeRegisteredEndpointMissing) {
		t.Errorf("err = %v, want code %s", err, CodeRegisteredEndpointMissing)
	}
}

func TestEngine_Build_PerEndpointHTTPClient_Overrides(t *testing.T) {
	e := New()
	overridesYAML := []byte(`clients:
  - name: c1
    base_url: https://example.com
    timeout: 10s
    endpoints:
      - {name: shared, method: GET, path: /a}
      - {name: special, method: GET, path: /b, timeout: 30s}
`)
	if err := e.LoadBytes(overridesYAML); err != nil {
		t.Fatal(err)
	}
	c, err := e.Build()
	if err != nil {
		t.Fatal(err)
	}
	shared := c.endpoints["c1.shared"]
	special := c.endpoints["c1.special"]
	if shared.httpClient == nil || special.httpClient == nil {
		t.Fatal("missing httpClient on resolvedEndpoint")
	}
	if shared.httpClient == special.httpClient {
		t.Error("special endpoint must have its own *http.Client (it overrides timeout)")
	}
}

func TestEngine_Build_PerEndpointHTTPClient_Shared(t *testing.T) {
	e := New()
	sharedYAML := []byte(`clients:
  - name: c1
    base_url: https://example.com
    endpoints:
      - {name: a, method: GET, path: /a}
      - {name: b, method: GET, path: /b}
`)
	if err := e.LoadBytes(sharedYAML); err != nil {
		t.Fatal(err)
	}
	c, err := e.Build()
	if err != nil {
		t.Fatal(err)
	}
	a := c.endpoints["c1.a"]
	b := c.endpoints["c1.b"]
	if a.httpClient != b.httpClient {
		t.Error("endpoints without overrides must share the per-client *http.Client")
	}
}

func TestResolveAuthHeader(t *testing.T) {
	tests := []struct {
		name      string
		a         *rawAuth
		wantName  string
		wantValue string
	}{
		{"nil", nil, "", ""},
		{"none", &rawAuth{Type: "none"}, "", ""},
		{"empty type", &rawAuth{Type: ""}, "", ""},
		{"basic", &rawAuth{Type: "basic", Username: "alice", Password: "wonderland"},
			"Authorization", "Basic YWxpY2U6d29uZGVybGFuZA=="},
		{"bearer", &rawAuth{Type: "bearer", Token: "tok-123"},
			"Authorization", "Bearer tok-123"},
		{"header", &rawAuth{Type: "header", Name: "X-API-Key", Value: "k-12345"},
			"X-API-Key", "k-12345"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotValue := resolveAuthHeader(tt.a)
			if gotName != tt.wantName || gotValue != tt.wantValue {
				t.Errorf("got (%q, %q), want (%q, %q)",
					gotName, gotValue, tt.wantName, tt.wantValue)
			}
		})
	}
}

func TestEngine_Build_AuthHeaderStoredOnResolvedEndpoint(t *testing.T) {
	t.Setenv("APIMAP_TEST_TOKEN", "tok-stored")
	e := New()
	authYAML := []byte(`clients:
  - name: gh
    base_url: https://api.github.com
    auth:
      type: bearer
      token: ${APIMAP_TEST_TOKEN}
    endpoints: [{name: a, method: GET, path: /a}]
`)
	if err := e.LoadBytes(authYAML); err != nil {
		t.Fatal(err)
	}
	c, err := e.Build()
	if err != nil {
		t.Fatal(err)
	}
	ep := c.endpoints["gh.a"]
	if ep.authHdrName != "Authorization" {
		t.Errorf("authHdrName = %q, want Authorization", ep.authHdrName)
	}
	if ep.authHdrValue != "Bearer tok-stored" {
		t.Errorf("authHdrValue = %q, want Bearer tok-stored", ep.authHdrValue)
	}
}

func TestEngine_Build_CustomAuth_UnknownSigner(t *testing.T) {
	e := New()
	yaml := []byte(`clients:
  - name: c1
    base_url: https://example.com
    auth:
      type: custom
      name: hmac
    endpoints: [{name: a, method: GET, path: /a}]
`)
	if err := e.LoadBytes(yaml); err != nil {
		t.Fatal(err)
	}
	// No RegisterAuth(e, "hmac", ...) → Build must surface the missing-signer error.
	_, err := e.Build()
	if err == nil {
		t.Fatal("expected build error, got nil")
	}
	var xe *xerrs.Error
	if !errors.As(err, &xe) || xe.Code != CodeUnknownCustomAuth {
		t.Fatalf("got %v, want CodeUnknownCustomAuth", err)
	}
}

func TestEngine_RegisterAuth_DuplicatePanics(t *testing.T) {
	e := New()
	RegisterAuth(e, "hmac", func(*http.Request) error { return nil })
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate RegisterAuth, got none")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("recover returned non-error %T", r)
		}
		var xe *xerrs.Error
		if !errors.As(err, &xe) || xe.Code != CodeDuplicateCustomAuth {
			t.Fatalf("got %v, want CodeDuplicateCustomAuth", err)
		}
	}()
	RegisterAuth(e, "hmac", func(*http.Request) error { return nil })
}

func TestEngine_Build_CustomAuth_MissingName(t *testing.T) {
	e := New()
	yaml := []byte(`clients:
  - name: c1
    base_url: https://example.com
    auth:
      type: custom
    endpoints: [{name: a, method: GET, path: /a}]
`)
	if err := e.LoadBytes(yaml); err != nil {
		t.Fatal(err)
	}
	_, err := e.Build()
	if err == nil {
		t.Fatal("expected build error, got nil")
	}
	var xe *xerrs.Error
	if !errors.As(err, &xe) || xe.Code != CodeAuthMissingField {
		t.Fatalf("got %v, want CodeAuthMissingField", err)
	}
}

// Silence unused import warning if http isn't used directly elsewhere.
var _ = http.DefaultTransport

func TestWithEnv_MapWinsOverProcessEnv(t *testing.T) {
	t.Setenv("APIMAP_TEST_KEY", "from-env")
	e := New(WithEnv(map[string]string{"APIMAP_TEST_KEY": "from-map"}))
	yaml := []byte(`clients:
  - name: x
    base_url: https://${APIMAP_TEST_KEY}.example.com
    endpoints:
      - {name: get, method: GET, path: /, decode: json}
`)
	if err := e.LoadBytes(yaml); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if got, want := e.clients[0].BaseURL, "https://from-map.example.com"; got != want {
		t.Fatalf("BaseURL: got %q want %q (map should win)", got, want)
	}
}

func TestWithEnv_FallsBackToProcessEnv(t *testing.T) {
	t.Setenv("APIMAP_TEST_KEY_X", "from-env-only")
	e := New(WithEnv(map[string]string{"OTHER_KEY": "y"}))
	yaml := []byte(`clients:
  - name: x
    base_url: https://${APIMAP_TEST_KEY_X}.example.com
    endpoints:
      - {name: get, method: GET, path: /, decode: json}
`)
	if err := e.LoadBytes(yaml); err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if got, want := e.clients[0].BaseURL, "https://from-env-only.example.com"; got != want {
		t.Fatalf("BaseURL: got %q want %q (process env fallback)", got, want)
	}
}

func TestWithEnv_BothMissingReturnsCodeEnvVarUnset(t *testing.T) {
	os.Unsetenv("APIMAP_TEST_KEY_MISSING")
	e := New(WithEnv(map[string]string{}))
	yaml := []byte(`clients:
  - name: x
    base_url: https://${APIMAP_TEST_KEY_MISSING}.example.com
    endpoints:
      - {name: get, method: GET, path: /, decode: json}
`)
	err := e.LoadBytes(yaml)
	if err == nil || !strings.Contains(err.Error(), CodeEnvVarUnset) {
		t.Fatalf("want CodeEnvVarUnset, got %v", err)
	}
}
