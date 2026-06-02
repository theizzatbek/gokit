package httpc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/breaker"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// newTestBreaker builds a breaker tuned for fast tests.
func newTestBreaker(t *testing.T) *breaker.Breaker {
	t.Helper()
	b, err := breaker.New(breaker.Config{
		Name:              "test",
		FailureThreshold:  3,
		MinimumRequests:   3,
		WindowDuration:    time.Second,
		WindowSize:        10,
		OpenInterval:      50 * time.Millisecond,
		HalfOpenMaxProbes: 1,
	})
	if err != nil {
		t.Fatalf("breaker.New: %v", err)
	}
	return b
}

func TestBreaker_TripsOnFiveHundreds(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := newTestBreaker(t)
	cfg := Config{
		Timeout:     200 * time.Millisecond,
		MaxRetries:  -1, // disable retry so each call is one attempt
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	}
	c, err := New(cfg, WithBreaker(b))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 3 failures trip the breaker (FailureThreshold=3, MinimumRequests=3).
	for i := 0; i < 3; i++ {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		resp.Body.Close()
	}
	if b.State() != breaker.StateOpen {
		t.Fatalf("breaker state = %v, want open", b.State())
	}
	if hits.Load() != 3 {
		t.Fatalf("server hits = %d, want 3", hits.Load())
	}

	// Next call must short-circuit — server hits must NOT increase.
	_, err = c.Get(srv.URL)
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
	if xe.Code != CodeCircuitOpen {
		t.Errorf("Code = %q, want %q", xe.Code, CodeCircuitOpen)
	}
	if xe.Kind != xerrs.KindUnavailable {
		t.Errorf("Kind = %v, want KindUnavailable", xe.Kind)
	}
	if hits.Load() != 3 {
		t.Errorf("server hits = %d after short-circuit, want 3", hits.Load())
	}
}

func TestBreaker_RetryDoesNotAmplifyOpenCircuit(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := newTestBreaker(t)
	cfg := Config{
		Timeout:     200 * time.Millisecond,
		MaxRetries:  5, // retry IS enabled — but ErrOpen must bypass it
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	}
	c, err := New(cfg, WithBreaker(b))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// One GET with MaxRetries=5 and a 500 server: first attempt hits,
	// second/third hit through retries until breaker trips, then the
	// remaining retries short-circuit without touching the server.
	resp, err := c.Get(srv.URL)
	if err != nil {
		// May or may not return an error depending on which attempt
		// the breaker trips — what matters is the server hit cap.
		_ = err
	} else {
		resp.Body.Close()
	}
	// 3 failures = trip threshold. After that every remaining
	// retry attempt must short-circuit. Cap is therefore 3.
	if hits.Load() > 3 {
		t.Errorf("server hits = %d, want <= 3 (retry must bail on ErrOpen)", hits.Load())
	}
	if b.State() != breaker.StateOpen {
		t.Errorf("breaker not open after trip: %v", b.State())
	}
}

func TestBreaker_RecoveryAfterOpenInterval(t *testing.T) {
	t.Parallel()
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b := newTestBreaker(t)
	cfg := Config{
		Timeout:     200 * time.Millisecond,
		MaxRetries:  -1,
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	}
	c, err := New(cfg, WithBreaker(b))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 3; i++ {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatalf("trip attempt %d: %v", i, err)
		}
		resp.Body.Close()
	}
	if b.State() != breaker.StateOpen {
		t.Fatalf("setup: not open")
	}

	// Wait past OpenInterval.
	time.Sleep(75 * time.Millisecond)
	fail.Store(false)

	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("recovery call: %v", err)
	}
	resp.Body.Close()
	if b.State() != breaker.StateClosed {
		t.Errorf("after successful probe: state = %v, want closed", b.State())
	}
}

func TestBreaker_ContextCanceledDoesNotTrip(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow handler — never responds before the caller's ctx is
		// cancelled in the test below.
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	b := newTestBreaker(t)
	cfg := Config{
		Timeout:     time.Second,
		MaxRetries:  -1,
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	}
	c, err := New(cfg, WithBreaker(b))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
		go func() {
			time.Sleep(5 * time.Millisecond)
			cancel()
		}()
		resp, err := c.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		cancel()
	}
	if b.State() != breaker.StateClosed {
		t.Errorf("after cancellations: state = %v, want closed", b.State())
	}
}

func TestBreaker_NonTransient4xxDoesNotTrip(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	b := newTestBreaker(t)
	cfg := Config{
		Timeout:     200 * time.Millisecond,
		MaxRetries:  -1,
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	}
	c, err := New(cfg, WithBreaker(b))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 10; i++ {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		resp.Body.Close()
	}
	if b.State() != breaker.StateClosed {
		t.Errorf("after 10x 404: state = %v, want closed (4xx is client error, not upstream)", b.State())
	}
}

func TestBreaker_CustomFailureClassifier(t *testing.T) {
	t.Parallel()
	// Treat 200 as failure to verify the classifier override actually
	// drives the breaker (not the default).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	b := newTestBreaker(t)
	cfg := Config{
		Timeout:     200 * time.Millisecond,
		MaxRetries:  -1,
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	}
	c, err := New(cfg,
		WithBreaker(b),
		WithBreakerFailureClassifier(func(resp *http.Response, err error) bool {
			return err == nil && resp != nil && resp.StatusCode == 200
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 3; i++ {
		resp, _ := c.Get(srv.URL)
		if resp != nil {
			resp.Body.Close()
		}
	}
	if b.State() != breaker.StateOpen {
		t.Errorf("custom classifier did not drive breaker: state = %v", b.State())
	}
}

func TestBreaker_NoBreakerNoChange(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := Config{
		Timeout:     200 * time.Millisecond,
		MaxRetries:  -1,
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	}
	// No WithBreaker — must behave exactly like before.
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < 5; i++ {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if resp.StatusCode != 500 {
			t.Errorf("status = %d, want 500", resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestDefaultBreakerFailure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		resp *http.Response
		err  error
		want bool
	}{
		{"nil err 200", &http.Response{StatusCode: 200}, nil, false},
		{"nil err 404", &http.Response{StatusCode: 404}, nil, false},
		{"nil err 500", &http.Response{StatusCode: 500}, nil, true},
		{"nil err 408", &http.Response{StatusCode: 408}, nil, true},
		{"nil err 429", &http.Response{StatusCode: 429}, nil, true},
		{"context canceled", nil, context.Canceled, false},
		{"deadline exceeded", nil, context.DeadlineExceeded, true},
		{"arbitrary error", nil, errors.New("boom"), true},
		{"nil resp + nil err", nil, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := defaultBreakerFailure(tc.resp, tc.err); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
