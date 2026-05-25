package httpc

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
