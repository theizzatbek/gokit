package apimap

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	xerrs "github.com/theizzatbek/fibermap/errs"
)

// buildClientWithYAML loads YAML with <BASE> replaced by srv.URL,
// builds the Client, and returns it.
func buildClientWithYAML(t *testing.T, yamlTmpl, baseURL string) *Client {
	t.Helper()
	yaml := strings.ReplaceAll(yamlTmpl, "<BASE>", baseURL)
	e := New()
	if err := e.LoadBytes([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	c, err := e.Build()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClient_Do_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/torvalds" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"login":"torvalds"}`))
	}))
	t.Cleanup(srv.Close)

	c := buildClientWithYAML(t, `clients:
  - name: gh
    base_url: <BASE>
    endpoints:
      - name: get_user
        method: GET
        path: /users/{username}
        decode: json
`, srv.URL)

	resp, err := c.Do(context.Background(), "gh.get_user",
		Call{Path: map[string]string{"username": "torvalds"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"login":"torvalds"}` {
		t.Errorf("body = %q", body)
	}
}

func TestClient_Do_UnknownEndpoint(t *testing.T) {
	c := buildClientWithYAML(t, `clients:
  - name: gh
    base_url: https://example.com
    endpoints: [{name: a, method: GET, path: /a}]
`, "")

	_, err := c.Do(context.Background(), "gh.nope", Call{})
	if err == nil {
		t.Fatal("nil error, want CodeUnknownEndpoint")
	}
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeUnknownEndpoint {
		t.Errorf("err = %v, want code %s", err, CodeUnknownEndpoint)
	}
}

func TestClient_Do_NonStatusErrorsAreStdlib(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	c := buildClientWithYAML(t, `clients:
  - name: gh
    base_url: <BASE>
    endpoints: [{name: a, method: GET, path: /a}]
`, srv.URL)

	resp, err := c.Do(context.Background(), "gh.a", Call{})
	if err != nil {
		t.Fatalf("Do returned error (should pass status through unchanged): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestClient_Do_QueryAndHeaders(t *testing.T) {
	var (
		gotQuery   string
		gotHeader1 string
		gotHeader2 string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotHeader1 = r.Header.Get("X-From-Default")
		gotHeader2 = r.Header.Get("X-From-Call")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := buildClientWithYAML(t, `clients:
  - name: c1
    base_url: <BASE>
    default_headers:
      X-From-Default: "yes"
    endpoints:
      - {name: a, method: GET, path: /a}
`, srv.URL)

	resp, err := c.Do(context.Background(), "c1.a", Call{
		Query:   map[string][]string{"limit": {"50"}},
		Headers: http.Header{"X-From-Call": []string{"from-call"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotQuery != "limit=50" {
		t.Errorf("query = %q", gotQuery)
	}
	if gotHeader1 != "yes" {
		t.Errorf("default header missing: %q", gotHeader1)
	}
	if gotHeader2 != "from-call" {
		t.Errorf("call header missing: %q", gotHeader2)
	}
}

func TestClient_Do_Auth_Bearer(t *testing.T) {
	t.Setenv("APIMAP_TEST_TOKEN", "secret-bearer")
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := buildClientWithYAML(t, `clients:
  - name: c1
    base_url: <BASE>
    auth:
      type: bearer
      token: ${APIMAP_TEST_TOKEN}
    endpoints: [{name: a, method: GET, path: /a}]
`, srv.URL)

	resp, err := c.Do(context.Background(), "c1.a", Call{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotAuth != "Bearer secret-bearer" {
		t.Errorf("Authorization = %q, want Bearer secret-bearer", gotAuth)
	}
}

func TestClient_Do_Auth_Basic(t *testing.T) {
	t.Setenv("APIMAP_TEST_USER", "alice")
	t.Setenv("APIMAP_TEST_PASS", "wonderland")
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := buildClientWithYAML(t, `clients:
  - name: c1
    base_url: <BASE>
    auth:
      type: basic
      username: ${APIMAP_TEST_USER}
      password: ${APIMAP_TEST_PASS}
    endpoints: [{name: a, method: GET, path: /a}]
`, srv.URL)

	resp, err := c.Do(context.Background(), "c1.a", Call{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// base64("alice:wonderland") = YWxpY2U6d29uZGVybGFuZA==
	if gotAuth != "Basic YWxpY2U6d29uZGVybGFuZA==" {
		t.Errorf("Authorization = %q, want Basic YWxpY2U6d29uZGVybGFuZA==", gotAuth)
	}
}

func TestClient_Do_Auth_CustomHeader(t *testing.T) {
	t.Setenv("APIMAP_TEST_KEY", "k-12345")
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := buildClientWithYAML(t, `clients:
  - name: c1
    base_url: <BASE>
    auth:
      type: header
      name: X-API-Key
      value: ${APIMAP_TEST_KEY}
    endpoints: [{name: a, method: GET, path: /a}]
`, srv.URL)

	resp, err := c.Do(context.Background(), "c1.a", Call{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotKey != "k-12345" {
		t.Errorf("X-API-Key = %q, want k-12345", gotKey)
	}
}

func TestClient_Do_Auth_NoneOmitted(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := buildClientWithYAML(t, `clients:
  - name: c1
    base_url: <BASE>
    endpoints: [{name: a, method: GET, path: /a}]
`, srv.URL)

	resp, err := c.Do(context.Background(), "c1.a", Call{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want empty (no auth declared)", gotAuth)
	}
}

func TestClient_Do_Auth_CallOverrides(t *testing.T) {
	t.Setenv("APIMAP_TEST_TOKEN", "original")
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := buildClientWithYAML(t, `clients:
  - name: c1
    base_url: <BASE>
    auth:
      type: bearer
      token: ${APIMAP_TEST_TOKEN}
    endpoints: [{name: a, method: GET, path: /a}]
`, srv.URL)

	resp, err := c.Do(context.Background(), "c1.a", Call{
		Headers: http.Header{"Authorization": []string{"Bearer override"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotAuth != "Bearer override" {
		t.Errorf("Authorization = %q, want Bearer override (call must override declared auth)", gotAuth)
	}
}

func TestClient_Do_PerEndpointHTTPClient_Used(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := buildClientWithYAML(t, `clients:
  - name: c1
    base_url: <BASE>
    endpoints:
      - {name: shared, method: GET, path: /a}
      - {name: special, method: GET, path: /a, timeout: 30s}
`, srv.URL)

	for _, ep := range []string{"c1.shared", "c1.special"} {
		resp, err := c.Do(context.Background(), ep, Call{})
		if err != nil {
			t.Fatalf("%s: %v", ep, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s: status = %d", ep, resp.StatusCode)
		}
	}
}
