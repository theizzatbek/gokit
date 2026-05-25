package httpc

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fastCfg is a Config tuned for tight test timing.
func fastCfg() Config {
	return Config{
		Timeout:     500 * time.Millisecond,
		MaxRetries:  0,
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	}
}

func newRetryTransport(t *testing.T, cfg Config, opts ...Option) *retryTransport {
	t.Helper()
	cfg.applyDefaults()
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}
	base := o.baseTransport
	if base == nil {
		base = http.DefaultTransport
	}
	return &retryTransport{
		base:        base,
		timeout:     cfg.Timeout,
		maxRetries:  cfg.MaxRetries,
		backoffBase: cfg.BackoffBase,
		backoffMax:  cfg.BackoffMax,
		logger:      o.logger,
	}
}

func TestRetryTransport_Success(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	rt := newRetryTransport(t, fastCfg())
	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Errorf("server saw %d requests, want 1", got)
	}
}

func TestRetryTransport_PerAttemptTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cfg := fastCfg()
	cfg.Timeout = 50 * time.Millisecond
	rt := newRetryTransport(t, cfg)
	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := rt.RoundTrip(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("RoundTrip succeeded, want timeout error")
	}
	if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		if !isDeadlineErr(err) {
			t.Errorf("err = %v (type %T), want timeout", err, err)
		}
	}
}

func TestRetryTransport_ContextCanceledBeforeStart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be hit")
	}))
	t.Cleanup(srv.Close)

	rt := newRetryTransport(t, fastCfg())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	if _, err := rt.RoundTrip(req); err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestRetryTransport_BodyReadableAfterRoundTrip(t *testing.T) {
	// Regression guard for the per-attempt-cancel pattern: the response body
	// must remain readable after RoundTrip returns, even though we used
	// context.WithTimeout internally.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("payload"))
	}))
	t.Cleanup(srv.Close)

	rt := newRetryTransport(t, fastCfg())
	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	resp.Body.Close()
	if string(body) != "payload" {
		t.Errorf("body = %q, want %q", body, "payload")
	}
}

func isDeadlineErr(err error) bool {
	for err != nil {
		if err == context.DeadlineExceeded {
			return true
		}
		type wrapper interface{ Unwrap() error }
		w, ok := err.(wrapper)
		if !ok {
			return false
		}
		err = w.Unwrap()
	}
	return false
}

func TestRetryTransport_RetryOnStatus(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&n, 1)
		if count < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cfg := fastCfg()
	cfg.MaxRetries = 3
	rt := newRetryTransport(t, cfg)
	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&n); got != 3 {
		t.Errorf("server saw %d requests, want 3 (1 initial + 2 retries)", got)
	}
}

func TestRetryTransport_ExhaustsRetries(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	cfg := fastCfg()
	cfg.MaxRetries = 2
	rt := newRetryTransport(t, cfg)
	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	want := int32(3) // 1 initial + 2 retries
	if got := atomic.LoadInt32(&n); got != want {
		t.Errorf("server saw %d requests, want %d", got, want)
	}
}

func TestRetryTransport_NonRetryableStatusReturnedImmediately(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	cfg := fastCfg()
	cfg.MaxRetries = 3
	rt := newRetryTransport(t, cfg)
	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Errorf("server saw %d requests, want 1 (400 must not retry)", got)
	}
}

func TestRetryTransport_NonIdempotentMethodSkipsRetry(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	cfg := fastCfg()
	cfg.MaxRetries = 3
	rt := newRetryTransport(t, cfg)
	req, _ := http.NewRequest("POST", srv.URL, nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Errorf("server saw %d requests, want 1 (POST must not retry)", got)
	}
}

func TestRetryTransport_NetworkErrorRetried(t *testing.T) {
	// Listen on a port, free it to force connection refused on every attempt.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg := fastCfg()
	cfg.MaxRetries = 2
	rt := newRetryTransport(t, cfg)
	req, _ := http.NewRequest("GET", "http://"+addr, nil)
	_, err = rt.RoundTrip(req)
	if err == nil {
		t.Fatal("RoundTrip succeeded, want network error after retries exhausted")
	}
	// We don't count attempts at the network layer here; the test verifies
	// that the network-error path returns an error (not a panic) after
	// the budget is exhausted. Attempt counting is covered via
	// TestRetryTransport_ExhaustsRetries for the status path.
}

func TestRetryTransport_BackoffRespected(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&n, 1)
		if count < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cfg := Config{
		Timeout:     time.Second,
		MaxRetries:  3,
		BackoffBase: 20 * time.Millisecond,
		BackoffMax:  50 * time.Millisecond,
	}
	rt := newRetryTransport(t, cfg)
	req, _ := http.NewRequest("GET", srv.URL, nil)
	start := time.Now()
	resp, err := rt.RoundTrip(req)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	if elapsed > 500*time.Millisecond {
		t.Errorf("elapsed = %v, want < 500ms", elapsed)
	}
}

func TestRetryTransport_ContextCancelDuringBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	cfg := Config{
		Timeout:     time.Second,
		MaxRetries:  10,
		BackoffBase: 200 * time.Millisecond,
		BackoffMax:  time.Second,
	}
	rt := newRetryTransport(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := rt.RoundTrip(req)
	elapsed := time.Since(start)
	if err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if elapsed > 250*time.Millisecond {
		t.Errorf("elapsed = %v, want < 250ms (cancel should abort backoff)", elapsed)
	}
}

func TestRetryTransport_RetryAfterSeconds(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&n, 1)
		if count == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cfg := fastCfg()
	cfg.MaxRetries = 1
	rt := newRetryTransport(t, cfg)
	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&n); got != 2 {
		t.Errorf("server saw %d requests, want 2", got)
	}
}

func TestRetryTransport_RetryAfterCapped(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&n, 1)
		if count == 1 {
			w.Header().Set("Retry-After", "9999")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cfg := Config{
		Timeout:     time.Second,
		MaxRetries:  1,
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	}
	rt := newRetryTransport(t, cfg)
	req, _ := http.NewRequest("GET", srv.URL, nil)
	start := time.Now()
	resp, err := rt.RoundTrip(req)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	if elapsed > 200*time.Millisecond {
		t.Errorf("elapsed = %v, want < 200ms (Retry-After should be capped at 4*BackoffMax=40ms)", elapsed)
	}
}

func TestRetryTransport_NoGetBody_NoRetryAfterFirstAttempt(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	cfg := fastCfg()
	cfg.MaxRetries = 3
	rt := newRetryTransport(t, cfg)
	// Build a PUT with a body but no GetBody (simulates a streaming body).
	// PUT is idempotent so it would otherwise retry on 503.
	req := &http.Request{
		Method: "PUT",
		URL:    mustParseURL(t, srv.URL),
		Body:   io.NopCloser(strings.NewReader("payload")),
		Header: http.Header{},
		// GetBody intentionally nil.
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Errorf("server saw %d requests, want 1 (no GetBody → no retry)", got)
	}
}

func TestRetryTransport_GetBodyReplayed(t *testing.T) {
	var bodies []string
	var mu sync.Mutex
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&n, 1)
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		if count < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cfg := fastCfg()
	cfg.MaxRetries = 2
	rt := newRetryTransport(t, cfg)
	// http.NewRequest with strings.Reader sets GetBody automatically.
	req, _ := http.NewRequest("PUT", srv.URL, strings.NewReader("payload"))
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || bodies[0] != "payload" || bodies[1] != "payload" {
		t.Errorf("bodies = %v, want both = \"payload\"", bodies)
	}
}

func mustParseURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestRetryTransport_MethodCaseInsensitive(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	cfg := fastCfg()
	cfg.MaxRetries = 2
	rt := newRetryTransport(t, cfg)
	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Method = "get" // lowercase
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	if got := atomic.LoadInt32(&n); got != 3 {
		t.Errorf("server saw %d requests, want 3 (lowercase \"get\" must be classified idempotent)", got)
	}
}
