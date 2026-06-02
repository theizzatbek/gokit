package httpc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theizzatbek/gokit/breaker"
	"github.com/theizzatbek/gokit/bulkhead"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// newSmallBulkhead is the tight-cap helper used across these tests.
func newSmallBulkhead(t *testing.T, maxConcurrent, maxQueue int, queueTimeout time.Duration) *bulkhead.Bulkhead {
	t.Helper()
	b, err := bulkhead.New(bulkhead.Config{
		Name:          "test",
		MaxConcurrent: maxConcurrent,
		MaxQueue:      maxQueue,
		QueueTimeout:  queueTimeout,
	})
	if err != nil {
		t.Fatalf("bulkhead.New: %v", err)
	}
	return b
}

func TestBulkhead_FastFailWhenSaturated(t *testing.T) {
	t.Parallel()
	// Server holds the request open until the test releases it — so
	// the first caller occupies the only slot for the duration.
	hold := make(chan struct{})
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-hold
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(hold) })

	b := newSmallBulkhead(t, 1, 0, 0) // 1 slot, no queue
	cfg := Config{
		Timeout:     2 * time.Second,
		MaxRetries:  -1,
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	}
	c, err := New(cfg, WithBulkhead(b))
	if err != nil {
		t.Fatal(err)
	}

	// Launch a caller that holds the slot.
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		resp, err := c.Get(srv.URL)
		if err == nil {
			resp.Body.Close()
		}
	}()

	// Wait until the in-flight request reaches the server.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if hits.Load() == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	// Second caller must fail-fast with httpc_bulkhead_full.
	_, err = c.Get(srv.URL)
	if err == nil {
		t.Fatal("expected error from second caller")
	}
	if !errors.Is(err, bulkhead.ErrBulkheadFull) {
		t.Errorf("want errors.Is(err, ErrBulkheadFull), got %v", err)
	}
	var xe *xerrs.Error
	if !errors.As(err, &xe) {
		t.Fatalf("want *errs.Error, got %T", err)
	}
	if xe.Code != CodeBulkheadFull {
		t.Errorf("Code = %q, want %q", xe.Code, CodeBulkheadFull)
	}
	if xe.Kind != xerrs.KindUnavailable {
		t.Errorf("Kind = %v, want KindUnavailable", xe.Kind)
	}
	if hits.Load() != 1 {
		t.Errorf("server hits after fast-fail = %d, want 1", hits.Load())
	}

	hold <- struct{}{}
	<-firstDone
}

func TestBulkhead_MaxConcurrentEnforced(t *testing.T) {
	t.Parallel()
	const cap = 3
	var inFlight atomic.Int64
	var peak atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	b := newSmallBulkhead(t, cap, 1000, 0)
	c, err := New(Config{
		Timeout:     2 * time.Second,
		MaxRetries:  -1,
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	}, WithBulkhead(b))
	if err != nil {
		t.Fatal(err)
	}

	const callers = 30
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := c.Get(srv.URL)
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()

	if peak.Load() > int64(cap) {
		t.Errorf("server saw peak concurrency %d, want <= %d", peak.Load(), cap)
	}
}

func TestBulkhead_QueueTimeoutMapsToKindTimeout(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hold
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(hold) })

	b := newSmallBulkhead(t, 1, 5, 20*time.Millisecond)
	c, err := New(Config{
		Timeout:     2 * time.Second,
		MaxRetries:  -1,
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	}, WithBulkhead(b))
	if err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		resp, err := c.Get(srv.URL)
		if err == nil {
			resp.Body.Close()
		}
	}()

	// Give the first caller a moment to occupy the slot.
	time.Sleep(20 * time.Millisecond)

	// Second caller waits; QueueTimeout (20ms) fires.
	_, err = c.Get(srv.URL)
	if !errors.Is(err, bulkhead.ErrQueueTimeout) {
		t.Errorf("want ErrQueueTimeout, got %v", err)
	}
	var xe *xerrs.Error
	if !errors.As(err, &xe) {
		t.Fatalf("want *errs.Error, got %T", err)
	}
	if xe.Code != CodeBulkheadQueueTimeout {
		t.Errorf("Code = %q, want %q", xe.Code, CodeBulkheadQueueTimeout)
	}
	if xe.Kind != xerrs.KindTimeout {
		t.Errorf("Kind = %v, want KindTimeout", xe.Kind)
	}

	hold <- struct{}{}
	<-firstDone
}

func TestBulkhead_RetryDoesNotAmplifyFull(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-hold
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(hold) })

	b := newSmallBulkhead(t, 1, 0, 0)
	c, err := New(Config{
		Timeout:     2 * time.Second,
		MaxRetries:  5, // retry is on — but ErrBulkheadFull must bypass
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	}, WithBulkhead(b))
	if err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		resp, err := c.Get(srv.URL)
		if err == nil {
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

	// Second caller: ErrBulkheadFull → retry must bail immediately,
	// not loop with backoff.
	start := time.Now()
	_, err = c.Get(srv.URL)
	elapsed := time.Since(start)
	if !errors.Is(err, bulkhead.ErrBulkheadFull) {
		t.Errorf("want ErrBulkheadFull, got %v", err)
	}
	// Bailout must be near-instant — far less than the time 5 retries
	// would take even at 10ms BackoffMax.
	if elapsed > 100*time.Millisecond {
		t.Errorf("elapsed = %v, retry did not bail on ErrBulkheadFull", elapsed)
	}

	hold <- struct{}{}
	<-firstDone
}

func TestBulkhead_NoBulkheadNoChange(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c, err := New(Config{
		Timeout:     200 * time.Millisecond,
		MaxRetries:  -1,
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 50; i++ {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		resp.Body.Close()
	}
}

func TestBulkhead_OpenBreakerDoesNotConsumeSlot(t *testing.T) {
	t.Parallel()
	// Server always 500s so the breaker trips.
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	br, err := breaker.New(breaker.Config{
		Name:              "test",
		FailureThreshold:  2,
		MinimumRequests:   2,
		WindowDuration:    time.Second,
		WindowSize:        10,
		OpenInterval:      30 * time.Second,
		HalfOpenMaxProbes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	bh := newSmallBulkhead(t, 1, 0, 0)
	c, err := New(Config{
		Timeout:     200 * time.Millisecond,
		MaxRetries:  -1,
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	},
		WithBreaker(br),
		WithBulkhead(bh),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Trip the breaker (2 calls hit the server; both consume + release
	// a slot since the breaker had not tripped yet).
	for i := 0; i < 2; i++ {
		resp, _ := c.Get(srv.URL)
		if resp != nil {
			resp.Body.Close()
		}
	}
	if br.State() != breaker.StateOpen {
		t.Fatalf("setup: breaker = %v, want open", br.State())
	}

	// While breaker is open, fire many calls — none should reach the
	// server, and the bulkhead must stay at zero in-flight (the slot
	// is acquired then immediately released because base returns
	// ErrOpen without doing the round-trip).
	for i := 0; i < 100; i++ {
		_, err := c.Get(srv.URL)
		if !errors.Is(err, breaker.ErrOpen) {
			t.Errorf("call %d: want ErrOpen, got %v", i, err)
		}
	}
	if hits.Load() != 2 {
		t.Errorf("server hits = %d, want 2 (open circuit must not reach server)", hits.Load())
	}
	if got := bh.Stats(); got.InFlight != 0 || got.Waiting != 0 {
		t.Errorf("bulkhead stats after open-circuit storm = %+v, want zero", got)
	}
}

func TestBulkhead_CtxCancelWhileWaiting(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hold
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(hold) })

	b := newSmallBulkhead(t, 1, 5, 0) // no QueueTimeout — only ctx
	c, err := New(Config{
		Timeout:     2 * time.Second,
		MaxRetries:  -1,
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
	}, WithBulkhead(b))
	if err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		resp, err := c.Get(srv.URL)
		if err == nil {
			resp.Body.Close()
		}
	}()
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	_, err = c.Do(req)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}

	hold <- struct{}{}
	<-firstDone
}
