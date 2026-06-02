package apimap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/breaker"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// fastBreakerYAML returns a YAML doc with a tight breaker config so
// tests trip quickly. Inline because every test wants slightly
// different httpd/path shapes.
const fastBreakerYAML = `clients:
  - name: gh
    base_url: <BASE>
    timeout: 200ms
    max_retries: -1
    breaker:
      failure_threshold: 3
      minimum_requests: 3
      window_duration: 1s
      window_size: 10
      open_interval: 50ms
      half_open_max_probes: 1
    endpoints:
      - name: get_user
        method: GET
        path: /users/{username}
        decode: json
`

func TestBreaker_TripsViaApimap(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c := buildClientWithYAML(t, fastBreakerYAML, srv.URL)

	for i := 0; i < 3; i++ {
		resp, err := c.Do(context.Background(), "gh.get_user",
			Call{Path: map[string]string{"username": "x"}})
		if err != nil {
			t.Fatalf("trip %d: %v", i, err)
		}
		resp.Body.Close()
	}
	if hits.Load() != 3 {
		t.Fatalf("server hits = %d, want 3", hits.Load())
	}

	// Next call must short-circuit through Do.
	_, err := c.Do(context.Background(), "gh.get_user",
		Call{Path: map[string]string{"username": "x"}})
	if err == nil {
		t.Fatal("expected error after trip")
	}
	if !errors.Is(err, breaker.ErrOpen) {
		t.Errorf("want errors.Is(err, breaker.ErrOpen), got %v", err)
	}
	var xe *xerrs.Error
	if !errors.As(err, &xe) {
		t.Fatalf("want *errs.Error, got %T", err)
	}
	wantCode := "apimap_gh_circuit_open"
	if xe.Code != wantCode {
		t.Errorf("Code = %q, want %q", xe.Code, wantCode)
	}
	if xe.Kind != xerrs.KindUnavailable {
		t.Errorf("Kind = %v, want KindUnavailable", xe.Kind)
	}
	if hits.Load() != 3 {
		t.Errorf("server hits after short-circuit = %d, want 3", hits.Load())
	}
}

func TestBreaker_RecoversAfterOpenInterval(t *testing.T) {
	t.Parallel()
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	c := buildClientWithYAML(t, fastBreakerYAML, srv.URL)
	for i := 0; i < 3; i++ {
		resp, _ := c.Do(context.Background(), "gh.get_user",
			Call{Path: map[string]string{"username": "x"}})
		if resp != nil {
			resp.Body.Close()
		}
	}

	time.Sleep(75 * time.Millisecond)
	fail.Store(false)

	resp, err := c.Do(context.Background(), "gh.get_user",
		Call{Path: map[string]string{"username": "x"}})
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestBreaker_SharedAcrossEndpointOverride(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	// `slow_user` has a per-endpoint timeout override → gets its
	// OWN *http.Client at Build, but MUST share the same breaker as
	// `get_user`. Tripping via `get_user` must short-circuit
	// `slow_user` too.
	yml := `clients:
  - name: gh
    base_url: <BASE>
    timeout: 200ms
    max_retries: -1
    breaker:
      failure_threshold: 3
      minimum_requests: 3
      window_duration: 1s
      window_size: 10
      open_interval: 50ms
      half_open_max_probes: 1
    endpoints:
      - name: get_user
        method: GET
        path: /users/{username}
      - name: slow_user
        method: GET
        path: /slow/{username}
        timeout: 500ms
`
	c := buildClientWithYAML(t, yml, srv.URL)

	for i := 0; i < 3; i++ {
		resp, _ := c.Do(context.Background(), "gh.get_user",
			Call{Path: map[string]string{"username": "x"}})
		if resp != nil {
			resp.Body.Close()
		}
	}
	if hits.Load() != 3 {
		t.Fatalf("setup hits = %d, want 3", hits.Load())
	}

	// slow_user has its own *http.Client (timeout override) but the
	// breaker is shared — must short-circuit.
	_, err := c.Do(context.Background(), "gh.slow_user",
		Call{Path: map[string]string{"username": "x"}})
	if !errors.Is(err, breaker.ErrOpen) {
		t.Errorf("slow_user did not see the shared breaker: %v", err)
	}
	if hits.Load() != 3 {
		t.Errorf("server hits after slow_user short-circuit = %d, want 3", hits.Load())
	}
}

func TestBreaker_NoBlockMeansNoBreaker(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	// No breaker: block — baseline regression check that the
	// integration is opt-in.
	yml := `clients:
  - name: gh
    base_url: <BASE>
    timeout: 200ms
    max_retries: -1
    endpoints:
      - name: get_user
        method: GET
        path: /users/{username}
`
	c := buildClientWithYAML(t, yml, srv.URL)
	for i := 0; i < 10; i++ {
		resp, _ := c.Do(context.Background(), "gh.get_user",
			Call{Path: map[string]string{"username": "x"}})
		if resp != nil {
			resp.Body.Close()
		}
	}
	if hits.Load() != 10 {
		t.Errorf("server hits = %d, want 10 (no breaker = no short-circuit)", hits.Load())
	}
}

func TestBreaker_InvalidYAMLBlock(t *testing.T) {
	t.Parallel()
	// MinimumRequests < FailureThreshold is a validation error from
	// breaker.New — must bubble up via Build with CodeInvalidBreaker.
	yml := `clients:
  - name: gh
    base_url: https://example.com
    breaker:
      failure_threshold: 50
      minimum_requests: 10
    endpoints:
      - name: get_user
        method: GET
        path: /users/{username}
`
	e := New()
	if err := e.LoadBytes([]byte(yml)); err != nil {
		t.Fatal(err)
	}
	_, err := e.Build()
	if err == nil {
		t.Fatal("expected Build to fail on invalid breaker")
	}
	var xe *xerrs.Error
	if !errors.As(err, &xe) {
		// errors.Join may wrap; check substring as fallback.
		if !strings.Contains(err.Error(), CodeInvalidBreaker) {
			t.Fatalf("err missing %q: %v", CodeInvalidBreaker, err)
		}
		return
	}
	if xe.Code != CodeInvalidBreaker {
		t.Errorf("Code = %q, want %q", xe.Code, CodeInvalidBreaker)
	}
}
