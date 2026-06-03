package httpc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ── ABC. Retry policy customization ────────────────────────────────────

func TestWithRetryClassifier_AllowsCustomStatus(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(423) // Locked — NOT in default set
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cli, err := New(Config{Timeout: time.Second, MaxRetries: 5, BackoffBase: 1 * time.Millisecond, BackoffMax: 2 * time.Millisecond},
		WithRetryClassifier(func(req *http.Request, resp *http.Response, err error) bool {
			if err != nil {
				return true
			}
			return resp != nil && resp.StatusCode == 423
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := cli.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3 (classifier should retry 423)", calls.Load())
	}
}

func TestWithRetryStatusCodes_OverridesDefault(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(429) // default-retryable
	}))
	defer srv.Close()

	cli, _ := New(Config{Timeout: time.Second, MaxRetries: 3, BackoffBase: 1 * time.Millisecond, BackoffMax: 2 * time.Millisecond},
		WithRetryStatusCodes(503, 504), // 429 removed
	)
	_, _ = cli.Get(srv.URL)
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1 (429 should no longer retry)", got)
	}
}

func TestWithIdempotencyKeyHeader_RetriesPOST(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 2 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cli, _ := New(Config{Timeout: time.Second, MaxRetries: 3, BackoffBase: 1 * time.Millisecond, BackoffMax: 2 * time.Millisecond},
		WithIdempotencyKeyHeader("Idempotency-Key"),
	)
	req, _ := http.NewRequest("POST", srv.URL, strings.NewReader("payload"))
	req.Header.Set("Idempotency-Key", "abc-123")
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2 (POST with Idempotency-Key should retry once)", got)
	}
}

func TestWithIdempotencyKeyHeader_NoHeader_NoRetryPOST(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
	}))
	defer srv.Close()

	cli, _ := New(Config{Timeout: time.Second, MaxRetries: 3, BackoffBase: 1 * time.Millisecond, BackoffMax: 2 * time.Millisecond},
		WithIdempotencyKeyHeader("Idempotency-Key"),
	)
	_, _ = cli.Post(srv.URL, "text/plain", strings.NewReader("p"))
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1 (POST w/o Idempotency-Key must not retry)", got)
	}
}

func TestWithRetryOnNonIdempotent_RetriesPOSTUnconditionally(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 2 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cli, _ := New(Config{Timeout: time.Second, MaxRetries: 3, BackoffBase: 1 * time.Millisecond, BackoffMax: 2 * time.Millisecond},
		WithRetryOnNonIdempotent(true),
	)
	resp, err := cli.Post(srv.URL, "text/plain", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
}

// ── D. Middleware chain ────────────────────────────────────────────────

func TestWithMiddleware_ChainOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var order []string
	mw := func(name string) func(http.RoundTripper) http.RoundTripper {
		return func(next http.RoundTripper) http.RoundTripper {
			return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				order = append(order, name)
				return next.RoundTrip(req)
			})
		}
	}

	cli, _ := New(Config{Timeout: time.Second, MaxRetries: -1},
		WithMiddleware(mw("outer"), mw("inner")),
	)
	_, _ = cli.Get(srv.URL)
	if len(order) != 2 || order[0] != "outer" || order[1] != "inner" {
		t.Errorf("order = %v, want [outer inner]", order)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// ── F. Lifecycle hooks ────────────────────────────────────────────────

func TestWithBeforeAfter_FireExactlyOncePerRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var beforeHits, afterHits atomic.Int32
	var sawElapsed atomic.Bool
	cli, _ := New(Config{Timeout: time.Second, MaxRetries: -1},
		WithBeforeRequest(func(r *http.Request) { beforeHits.Add(1) }),
		WithAfterResponse(func(_ *http.Request, _ *http.Response, _ error, d time.Duration) {
			afterHits.Add(1)
			if d > 0 {
				sawElapsed.Store(true)
			}
		}),
	)
	_, _ = cli.Get(srv.URL)
	if beforeHits.Load() != 1 || afterHits.Load() != 1 {
		t.Errorf("hits before=%d after=%d, want 1/1", beforeHits.Load(), afterHits.Load())
	}
	if !sawElapsed.Load() {
		t.Error("elapsed duration was zero")
	}
}

func TestWithAfterResponse_FiresEvenOnNetworkError(t *testing.T) {
	cli, _ := New(Config{Timeout: 50 * time.Millisecond, MaxRetries: -1})

	var fired atomic.Bool
	cli, _ = New(Config{Timeout: 50 * time.Millisecond, MaxRetries: -1},
		WithAfterResponse(func(req *http.Request, resp *http.Response, err error, d time.Duration) {
			if err != nil && resp == nil {
				fired.Store(true)
			}
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:1/no-listener", nil)
	_, _ = cli.Do(req)
	if !fired.Load() {
		t.Error("afterResponse should fire on network error")
	}
	_ = errors.New
}
