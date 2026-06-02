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

	"github.com/theizzatbek/gokit/bulkhead"
	xerrs "github.com/theizzatbek/gokit/errs"
)

const fastBulkheadYAML = `clients:
  - name: gh
    base_url: <BASE>
    timeout: 2s
    max_retries: -1
    bulkhead:
      max_concurrent: 1
      max_queue: 0
    endpoints:
      - name: get_user
        method: GET
        path: /users/{username}
        decode: json
`

func TestBulkhead_TripsViaApimap(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-hold
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(hold) })

	c := buildClientWithYAML(t, fastBulkheadYAML, srv.URL)

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		resp, _ := c.Do(context.Background(), "gh.get_user",
			Call{Path: map[string]string{"username": "x"}})
		if resp != nil {
			resp.Body.Close()
		}
	}()

	// Wait until the first request is in-flight.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if hits.Load() == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	_, err := c.Do(context.Background(), "gh.get_user",
		Call{Path: map[string]string{"username": "x"}})
	if err == nil {
		t.Fatal("expected error from saturated bulkhead")
	}
	if !errors.Is(err, bulkhead.ErrBulkheadFull) {
		t.Errorf("want errors.Is(err, ErrBulkheadFull), got %v", err)
	}
	var xe *xerrs.Error
	if !errors.As(err, &xe) {
		t.Fatalf("want *errs.Error, got %T", err)
	}
	wantCode := "apimap_gh_bulkhead_full"
	if xe.Code != wantCode {
		t.Errorf("Code = %q, want %q", xe.Code, wantCode)
	}
	if xe.Kind != xerrs.KindUnavailable {
		t.Errorf("Kind = %v, want KindUnavailable", xe.Kind)
	}
	if hits.Load() != 1 {
		t.Errorf("server hits = %d, want 1", hits.Load())
	}

	hold <- struct{}{}
	<-firstDone
}

func TestBulkhead_QueueTimeoutSurfacesAsKindTimeout(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hold
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(hold) })

	yml := `clients:
  - name: gh
    base_url: <BASE>
    timeout: 2s
    max_retries: -1
    bulkhead:
      max_concurrent: 1
      max_queue: 5
      queue_timeout: 20ms
    endpoints:
      - name: get_user
        method: GET
        path: /users/{u}
        decode: json
`
	c := buildClientWithYAML(t, yml, srv.URL)

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		resp, _ := c.Do(context.Background(), "gh.get_user",
			Call{Path: map[string]string{"u": "x"}})
		if resp != nil {
			resp.Body.Close()
		}
	}()
	time.Sleep(20 * time.Millisecond)

	_, err := c.Do(context.Background(), "gh.get_user",
		Call{Path: map[string]string{"u": "x"}})
	if !errors.Is(err, bulkhead.ErrQueueTimeout) {
		t.Errorf("want ErrQueueTimeout, got %v", err)
	}
	var xe *xerrs.Error
	if !errors.As(err, &xe) {
		t.Fatalf("want *errs.Error, got %T", err)
	}
	if xe.Code != "apimap_gh_bulkhead_queue_timeout" {
		t.Errorf("Code = %q", xe.Code)
	}
	if xe.Kind != xerrs.KindTimeout {
		t.Errorf("Kind = %v, want KindTimeout", xe.Kind)
	}

	hold <- struct{}{}
	<-firstDone
}

func TestBulkhead_SharedAcrossEndpointOverride(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-hold
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(hold) })

	// `slow_user` has a per-endpoint timeout override → its own
	// *http.Client. Must share the same bulkhead as `get_user`.
	yml := `clients:
  - name: gh
    base_url: <BASE>
    timeout: 2s
    max_retries: -1
    bulkhead:
      max_concurrent: 1
      max_queue: 0
    endpoints:
      - name: get_user
        method: GET
        path: /users/{u}
      - name: slow_user
        method: GET
        path: /slow/{u}
        timeout: 5s
`
	c := buildClientWithYAML(t, yml, srv.URL)

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		resp, _ := c.Do(context.Background(), "gh.get_user",
			Call{Path: map[string]string{"u": "x"}})
		if resp != nil {
			resp.Body.Close()
		}
	}()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if hits.Load() == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	// slow_user uses a different *http.Client (timeout override) but
	// must hit the same per-client bulkhead.
	_, err := c.Do(context.Background(), "gh.slow_user",
		Call{Path: map[string]string{"u": "x"}})
	if !errors.Is(err, bulkhead.ErrBulkheadFull) {
		t.Errorf("slow_user did not see the shared bulkhead: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("server hits = %d, want 1", hits.Load())
	}

	hold <- struct{}{}
	<-firstDone
}

func TestBulkhead_NoBlockMeansNoBulkhead(t *testing.T) {
	t.Parallel()
	var concurrent atomic.Int64
	var peak atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := concurrent.Add(1)
		defer concurrent.Add(-1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	yml := `clients:
  - name: gh
    base_url: <BASE>
    timeout: 2s
    max_retries: -1
    endpoints:
      - name: get_user
        method: GET
        path: /users/{u}
`
	c := buildClientWithYAML(t, yml, srv.URL)

	const callers = 20
	done := make(chan struct{}, callers)
	for i := 0; i < callers; i++ {
		go func() {
			resp, _ := c.Do(context.Background(), "gh.get_user",
				Call{Path: map[string]string{"u": "x"}})
			if resp != nil {
				resp.Body.Close()
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < callers; i++ {
		<-done
	}
	// No bulkhead → server should have seen real concurrency.
	if peak.Load() < 2 {
		t.Errorf("without bulkhead: peak concurrency = %d, expected > 1", peak.Load())
	}
}

func TestBulkhead_InvalidYAMLBlock(t *testing.T) {
	t.Parallel()
	// max_concurrent: 0 fails bulkhead.New validation.
	yml := `clients:
  - name: gh
    base_url: https://example.com
    bulkhead:
      max_concurrent: 0
    endpoints:
      - name: get_user
        method: GET
        path: /users/{u}
`
	e := New()
	if err := e.LoadBytes([]byte(yml)); err != nil {
		t.Fatal(err)
	}
	_, err := e.Build()
	if err == nil {
		t.Fatal("expected Build to fail")
	}
	var xe *xerrs.Error
	if !errors.As(err, &xe) {
		if !strings.Contains(err.Error(), CodeInvalidBulkhead) {
			t.Fatalf("err missing %q: %v", CodeInvalidBulkhead, err)
		}
		return
	}
	if xe.Code != CodeInvalidBulkhead {
		t.Errorf("Code = %q, want %q", xe.Code, CodeInvalidBulkhead)
	}
}
