package webhooks

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/propagation"

	"github.com/theizzatbek/gokit/breaker"
	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// in-memory store implementing DeliveryStore + tracking calls.
type memStore struct {
	mu        sync.Mutex
	pending   []Delivery
	delivered []uuid.UUID
	failed    []failed
	dlq       []dlq
}

type failed struct {
	id     uuid.UUID
	status int
	msg    string
	next   time.Time
}

type dlq struct {
	id     uuid.UUID
	status int
	msg    string
}

func (s *memStore) Enqueue(context.Context, db.Querier, []Delivery) error { return nil }

func (s *memStore) Claim(ctx context.Context, batchSize int) ([]Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if batchSize <= 0 || len(s.pending) == 0 {
		return nil, nil
	}
	n := batchSize
	if n > len(s.pending) {
		n = len(s.pending)
	}
	out := append([]Delivery(nil), s.pending[:n]...)
	s.pending = s.pending[n:]
	return out, nil
}

func (s *memStore) MarkDelivered(_ context.Context, id uuid.UUID, _ int) error {
	s.mu.Lock()
	s.delivered = append(s.delivered, id)
	s.mu.Unlock()
	return nil
}

func (s *memStore) MarkFailed(_ context.Context, id uuid.UUID, status int, msg string, next time.Time) error {
	s.mu.Lock()
	s.failed = append(s.failed, failed{id, status, msg, next})
	s.mu.Unlock()
	return nil
}

func (s *memStore) MarkDLQ(_ context.Context, id uuid.UUID, status int, msg string) error {
	s.mu.Lock()
	s.dlq = append(s.dlq, dlq{id, status, msg})
	s.mu.Unlock()
	return nil
}

// (Worker only consumes Claim / MarkDelivered / MarkFailed / MarkDLQ;
// Enqueue stays unused in these tests. dStore below satisfies the
// full DeliveryStore interface.)

// ── BCD: Custom classifier + hooks + attempt timeout ──────────────────

func TestWorker_RetryClassifierOverride_TermsValidationErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(422) // Unprocessable Entity — custom classifier will Term it
	}))
	defer srv.Close()

	d := Delivery{
		ID:             uuid.New(),
		SubscriptionID: uuid.New(),
		TargetURL:      srv.URL,
		Secret:         "s",
		Payload:        []byte(`{}`),
		Status:         DeliveryPending,
	}
	ms := &memStore{pending: []Delivery{d}}

	w, err := NewWorker(WorkerConfig{
		DeliveryStore: dStore{ms},
		HTTPClient:    srv.Client(),
		Interval:      10 * time.Millisecond,
		MaxAttempts:   3,
		RetryClassifier: func(resp *http.Response, err error) Outcome {
			if resp != nil && resp.StatusCode == 422 {
				return OutcomeFatal // app treats 422 as terminal
			}
			return DefaultClassifier(resp, err)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.drain(context.Background())
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if len(ms.dlq) != 1 {
		t.Fatalf("dlq = %d, want 1 (422 → fatal via custom classifier)", len(ms.dlq))
	}
}

func TestWorker_OnAttemptHook_Fires(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := Delivery{
		ID:             uuid.New(),
		SubscriptionID: uuid.New(),
		TargetURL:      srv.URL,
		Secret:         "s",
		Payload:        []byte(`{}`),
		Status:         DeliveryPending,
	}
	ms := &memStore{pending: []Delivery{d}}

	var hooks atomic.Int32
	var sawDelivered atomic.Bool

	w, _ := NewWorker(WorkerConfig{
		DeliveryStore: dStore{ms},
		HTTPClient:    srv.Client(),
		Interval:      10 * time.Millisecond,
		MaxAttempts:   3,
		OnAttempt: func(d Delivery, resp *http.Response, _ error, outcome Outcome, _ time.Duration) {
			hooks.Add(1)
			if outcome == OutcomeDelivered {
				sawDelivered.Store(true)
			}
		},
	})
	w.drain(context.Background())

	if hooks.Load() != 1 {
		t.Errorf("hooks = %d, want 1", hooks.Load())
	}
	if !sawDelivered.Load() {
		t.Error("OnAttempt should have seen OutcomeDelivered")
	}
}

func TestWorker_OnDLQHook_FiresOnFatalStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404) // default classifier → fatal
	}))
	defer srv.Close()

	d := Delivery{
		ID:             uuid.New(),
		SubscriptionID: uuid.New(),
		TargetURL:      srv.URL,
		Secret:         "s",
		Payload:        []byte(`{}`),
		Status:         DeliveryPending,
	}
	ms := &memStore{pending: []Delivery{d}}

	var dlqHit atomic.Int32
	w, _ := NewWorker(WorkerConfig{
		DeliveryStore: dStore{ms},
		HTTPClient:    srv.Client(),
		Interval:      10 * time.Millisecond,
		MaxAttempts:   3,
		OnDLQ: func(d Delivery, status int, _ string) {
			dlqHit.Add(1)
		},
	})
	w.drain(context.Background())
	if dlqHit.Load() != 1 {
		t.Errorf("OnDLQ hits = %d, want 1", dlqHit.Load())
	}
}

func TestWorker_AttemptTimeout_AppliesPerAttempt(t *testing.T) {
	// Slow handler — sleep 200ms; AttemptTimeout=50ms cuts it off as
	// retryable (network err / ctx canceled).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(200 * time.Millisecond):
			w.WriteHeader(200)
		}
	}))
	defer srv.Close()

	d := Delivery{
		ID:             uuid.New(),
		SubscriptionID: uuid.New(),
		TargetURL:      srv.URL,
		Secret:         "s",
		Payload:        []byte(`{}`),
		Status:         DeliveryPending,
	}
	ms := &memStore{pending: []Delivery{d}}

	w, _ := NewWorker(WorkerConfig{
		DeliveryStore:  dStore{ms},
		HTTPClient:     srv.Client(),
		Interval:       10 * time.Millisecond,
		MaxAttempts:    3,
		AttemptTimeout: 50 * time.Millisecond,
	})
	w.drain(context.Background())

	ms.mu.Lock()
	defer ms.mu.Unlock()
	if len(ms.failed) != 1 {
		t.Errorf("failed = %d, want 1 (attempt should have timed out)", len(ms.failed))
	}
}

// ── A. Per-subscription breaker ────────────────────────────────────────

func TestWorker_BreakerOpen_SchedulesRetryWithoutHittingNetwork(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()

	br, err := breaker.New(breaker.Config{
		Name:             "sub-1",
		FailureThreshold: 1,
		MinimumRequests:  1,
		OpenInterval:     time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pre-trip the breaker.
	_, done := br.Allow()
	done(false)
	if allowed, _ := br.Allow(); allowed {
		t.Fatal("test setup: breaker should be open after one failure with threshold=1")
	}

	subID := uuid.New()
	d := Delivery{
		ID:             uuid.New(),
		SubscriptionID: subID,
		TargetURL:      srv.URL,
		Secret:         "s",
		Payload:        []byte(`{}`),
		Status:         DeliveryPending,
	}
	ms := &memStore{pending: []Delivery{d}}

	w, _ := NewWorker(WorkerConfig{
		DeliveryStore: dStore{ms},
		HTTPClient:    srv.Client(),
		Interval:      10 * time.Millisecond,
		MaxAttempts:   3,
		BreakerFactory: func(id uuid.UUID) *breaker.Breaker {
			if id == subID {
				return br
			}
			return nil
		},
	})
	w.drain(context.Background())

	if hits.Load() != 0 {
		t.Errorf("upstream hits = %d, want 0 (breaker should short-circuit)", hits.Load())
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if len(ms.failed) != 1 {
		t.Errorf("failed = %d, want 1 (delivery rescheduled)", len(ms.failed))
	}
}

// ── G. Trace propagation ──────────────────────────────────────────────

func TestWorker_PropagatorInjectsTraceparent(t *testing.T) {
	var sawTP atomic.Value
	sawTP.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawTP.Store(r.Header.Get("Traceparent"))
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := Delivery{
		ID:             uuid.New(),
		SubscriptionID: uuid.New(),
		TargetURL:      srv.URL,
		Secret:         "s",
		Payload:        []byte(`{}`),
		Status:         DeliveryPending,
	}
	ms := &memStore{pending: []Delivery{d}}

	w, _ := NewWorker(WorkerConfig{
		DeliveryStore: dStore{ms},
		HTTPClient:    srv.Client(),
		Interval:      10 * time.Millisecond,
		MaxAttempts:   3,
		// Use a propagator that stamps a fixed header so the test
		// doesn't need a live tracer.
		Propagator: stampPropagator{},
	})
	w.drain(context.Background())
	if got := sawTP.Load().(string); got == "" {
		t.Error("Propagator did not inject traceparent")
	}
}

// stampPropagator is a minimal TextMapPropagator that always stamps
// `Traceparent: test-trace`.
type stampPropagator struct{}

func (stampPropagator) Inject(ctx context.Context, carrier propagation.TextMapCarrier) {
	carrier.Set("Traceparent", "test-trace")
}
func (stampPropagator) Extract(ctx context.Context, _ propagation.TextMapCarrier) context.Context {
	return ctx
}
func (stampPropagator) Fields() []string { return []string{"Traceparent"} }

// ── HK. Custom signer + default content-type ──────────────────────────

func TestWorker_CustomSignerAndContentType(t *testing.T) {
	var sawSig, sawCT atomic.Value
	sawSig.Store("")
	sawCT.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawSig.Store(r.Header.Get(SignatureHeader))
		sawCT.Store(r.Header.Get("Content-Type"))
		w.WriteHeader(200)
	}))
	defer srv.Close()

	d := Delivery{
		ID:             uuid.New(),
		SubscriptionID: uuid.New(),
		TargetURL:      srv.URL,
		Secret:         "s",
		Payload:        []byte(`raw-bytes`),
		Status:         DeliveryPending,
	}
	ms := &memStore{pending: []Delivery{d}}

	w, _ := NewWorker(WorkerConfig{
		DeliveryStore: dStore{ms},
		HTTPClient:    srv.Client(),
		Interval:      10 * time.Millisecond,
		MaxAttempts:   3,
		SignerFunc: func(body []byte, secret string, _ time.Time) (string, error) {
			return "custom:" + string(body) + ":" + secret, nil
		},
		DefaultContentType: "application/protobuf",
	})
	w.drain(context.Background())

	if got := sawSig.Load().(string); got != "custom:raw-bytes:s" {
		t.Errorf("X-Webhook-Signature = %q, want custom:raw-bytes:s", got)
	}
	if got := sawCT.Load().(string); got != "application/protobuf" {
		t.Errorf("Content-Type = %q, want application/protobuf", got)
	}
}

// ── EF. Panic recovery + Checker ──────────────────────────────────────

func TestWorker_AttemptPanicRecovered(t *testing.T) {
	// Custom RoundTripper that panics — the worker should recover
	// and reschedule.
	d := Delivery{
		ID:             uuid.New(),
		SubscriptionID: uuid.New(),
		TargetURL:      "http://example.invalid",
		Secret:         "s",
		Payload:        []byte(`{}`),
		Status:         DeliveryPending,
	}
	ms := &memStore{pending: []Delivery{d}}
	cli := &http.Client{Transport: panicTransport{}}

	w, _ := NewWorker(WorkerConfig{
		DeliveryStore: dStore{ms},
		HTTPClient:    cli,
		Interval:      10 * time.Millisecond,
		MaxAttempts:   3,
	})
	w.drain(context.Background())

	ms.mu.Lock()
	defer ms.mu.Unlock()
	if len(ms.failed) != 1 {
		t.Errorf("failed = %d, want 1 (panic should reschedule)", len(ms.failed))
	}
}

type panicTransport struct{}

func (panicTransport) RoundTrip(*http.Request) (*http.Response, error) { panic("boom") }

func TestChecker_NilReceiverSafe(t *testing.T) {
	var c *Checker
	err := c.Check(context.Background())
	var e *xerrs.Error
	if !errors.As(err, &e) || e.Code != CodeNotReady {
		t.Errorf("err = %v, want %q", err, CodeNotReady)
	}
}

func TestChecker_OKWhenStoreReturnsEmpty(t *testing.T) {
	ms := &memStore{} // no pending → Claim(0) returns nil
	c := NewChecker(dStore{ms}, "test")
	if err := c.Check(context.Background()); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

// dStore satisfies the DeliveryStore interface by forwarding to
// memStore + supplying a stub Enqueue with the right signature.
type dStore struct{ s *memStore }

func (d dStore) Enqueue(_ context.Context, _ db.Querier, _ []Delivery) error {
	return errors.New("unused")
}
func (d dStore) Claim(ctx context.Context, batchSize int) ([]Delivery, error) {
	return d.s.Claim(ctx, batchSize)
}
func (d dStore) MarkDelivered(ctx context.Context, id uuid.UUID, status int) error {
	return d.s.MarkDelivered(ctx, id, status)
}
func (d dStore) MarkFailed(ctx context.Context, id uuid.UUID, status int, msg string, next time.Time) error {
	return d.s.MarkFailed(ctx, id, status, msg, next)
}
func (d dStore) MarkDLQ(ctx context.Context, id uuid.UUID, status int, msg string) error {
	return d.s.MarkDLQ(ctx, id, status, msg)
}
