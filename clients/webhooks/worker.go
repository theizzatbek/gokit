package webhooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/propagation"

	"github.com/theizzatbek/gokit/breaker"
)

// Outcome classifies a Worker attempt result. Used by
// [WorkerConfig.RetryClassifier] to override the default
// 2xx/408/429/5xx → retryable, other 4xx → fatal mapping.
type Outcome int

const (
	OutcomeDelivered Outcome = iota
	OutcomeRetryable
	OutcomeFatal
)

// String renders Outcome for log attrs.
func (o Outcome) String() string {
	switch o {
	case OutcomeDelivered:
		return "delivered"
	case OutcomeRetryable:
		return "retryable"
	case OutcomeFatal:
		return "fatal"
	default:
		return "unknown"
	}
}

// DefaultClassifier is the kit's default classifier — exported so
// custom [WorkerConfig.RetryClassifier] implementations can fall
// back to it for unhandled status codes.
//
// 2xx → delivered, 408/429/5xx → retryable, everything else → fatal.
// A nil response (network failure) is treated as retryable.
func DefaultClassifier(resp *http.Response, err error) Outcome {
	if err != nil || resp == nil {
		return OutcomeRetryable
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return OutcomeDelivered
	case resp.StatusCode == 408 || resp.StatusCode == 429 ||
		(resp.StatusCode >= 500 && resp.StatusCode < 600):
		return OutcomeRetryable
	default:
		return OutcomeFatal
	}
}

// BackoffConfig controls per-delivery retry timing.
type BackoffConfig struct {
	Initial    time.Duration // first failure waits this long; default 1s
	Max        time.Duration // cap on per-attempt delay; default 1h
	Multiplier float64       // exponent base; default 2.0
	Jitter     float64       // 0..1, ratio of randomness added; default 0
}

func (b BackoffConfig) attemptDelay(attempt int) time.Duration {
	if b.Initial == 0 {
		b.Initial = time.Second
	}
	if b.Max == 0 {
		b.Max = time.Hour
	}
	if b.Multiplier == 0 {
		b.Multiplier = 2.0
	}
	d := float64(b.Initial) * math.Pow(b.Multiplier, float64(attempt-1))
	if d > float64(b.Max) {
		d = float64(b.Max)
	}
	if b.Jitter > 0 {
		spread := b.Jitter * d
		d = d - spread + rand.Float64()*spread
	}
	return time.Duration(d)
}

// SignerFunc is the pluggable signature shape consumed by Worker.
// Defaults to Stripe-style `t=<unix>,v1=<hmac-sha256>` via Signer.Sign.
// Implementations MUST be safe for concurrent use.
type SignerFunc func(body []byte, secret string, now time.Time) (string, error)

// OnAttemptFn fires after each Worker attempt (success or failure).
// resp may be nil when err != nil (network failure). outcome is the
// classifier's verdict for the (resp, err) pair.
type OnAttemptFn func(d Delivery, resp *http.Response, err error, outcome Outcome, elapsed time.Duration)

// OnDLQFn fires when a delivery transitions to the DLQ — either by
// hitting MaxAttempts or a fatal classifier verdict.
type OnDLQFn func(d Delivery, statusCode int, errMsg string)

// WorkerConfig wires DeliveryWorker dependencies.
type WorkerConfig struct {
	SubStore      SubscriptionStore
	DeliveryStore DeliveryStore
	HTTPClient    *http.Client
	MaxAttempts   int
	Backoff       BackoffConfig
	BatchSize     int
	Interval      time.Duration
	MaxInFlight   int
	Logger        *slog.Logger
	Metrics       prometheus.Registerer

	// AttemptTimeout bounds a single HTTP attempt. Default 30s.
	AttemptTimeout time.Duration

	// RetryClassifier overrides the kit's 2xx/408/429/5xx mapping
	// when non-nil. Use DefaultClassifier as a baseline.
	RetryClassifier func(*http.Response, error) Outcome

	// BreakerFactory, when non-nil, is consulted ONCE per
	// subscription ID; the returned *breaker.Breaker is cached and
	// reused. Returning nil disables the breaker for that subscription.
	// An open breaker short-circuits the attempt as retryable so the
	// delivery reschedules without burning the in-flight slot on a
	// known-down endpoint.
	BreakerFactory func(subscriptionID uuid.UUID) *breaker.Breaker

	// SignerFunc swaps the default Stripe-style signature. Result
	// is written to the X-Webhook-Signature header verbatim.
	SignerFunc SignerFunc

	// DefaultContentType overrides the hardcoded `application/json`.
	// Delivery.Headers still win on a per-call basis.
	DefaultContentType string

	// OnAttempt + OnDLQ are observability hooks. nil = silent.
	OnAttempt OnAttemptFn
	OnDLQ     OnDLQFn

	// Propagator, when non-nil, injects W3C TraceContext onto
	// outbound headers. Pass otel.GetTextMapPropagator() to enable;
	// nil = disabled.
	Propagator propagation.TextMapPropagator
}

// Worker drains pending deliveries and dispatches them.
type Worker struct {
	cfg     WorkerConfig
	signer  *Signer
	stopCh  chan struct{}
	doneCh  chan struct{}
	wg      sync.WaitGroup
	metrics *workerMetrics

	// breakerCache memoises per-subscription breakers built via
	// cfg.BreakerFactory.
	breakerCache sync.Map // uuid.UUID → *breaker.Breaker
}

// NewWorker validates config and returns a Worker ready to Start.
func NewWorker(cfg WorkerConfig) (*Worker, error) {
	if cfg.DeliveryStore == nil {
		return nil, errors.New("webhooks: WorkerConfig.DeliveryStore required")
	}
	if cfg.HTTPClient == nil {
		return nil, errors.New("webhooks: WorkerConfig.HTTPClient required")
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 8
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 32
	}
	if cfg.Interval == 0 {
		cfg.Interval = 500 * time.Millisecond
	}
	if cfg.MaxInFlight == 0 {
		cfg.MaxInFlight = 16
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.AttemptTimeout == 0 {
		cfg.AttemptTimeout = 30 * time.Second
	}
	if cfg.DefaultContentType == "" {
		cfg.DefaultContentType = "application/json"
	}
	w := &Worker{
		cfg:    cfg,
		signer: &Signer{},
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	w.metrics = newWorkerMetrics(cfg.Metrics)
	return w, nil
}

// breakerFor returns the cached *breaker.Breaker for sid or builds
// one via cfg.BreakerFactory. Returns nil when no factory is wired.
func (w *Worker) breakerFor(sid uuid.UUID) *breaker.Breaker {
	if w.cfg.BreakerFactory == nil {
		return nil
	}
	if v, ok := w.breakerCache.Load(sid); ok {
		return v.(*breaker.Breaker)
	}
	br := w.cfg.BreakerFactory(sid)
	actual, _ := w.breakerCache.LoadOrStore(sid, br)
	return actual.(*breaker.Breaker)
}

// sign invokes the configured SignerFunc or falls back to the kit
// Stripe-style Signer.
func (w *Worker) sign(body []byte, secret string, now time.Time) (string, error) {
	if w.cfg.SignerFunc != nil {
		return w.cfg.SignerFunc(body, secret, now)
	}
	return w.signer.Sign(body, secret, now)
}

// classify routes through the configured classifier or the kit
// default.
func (w *Worker) classify(resp *http.Response, err error) Outcome {
	if w.cfg.RetryClassifier != nil {
		return w.cfg.RetryClassifier(resp, err)
	}
	return DefaultClassifier(resp, err)
}

// Start launches the drain loop in a background goroutine.
func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	go w.loop(ctx)
}

// Stop signals the loop to exit and waits until it drains or ctx
// expires.
func (w *Worker) Stop(ctx context.Context) error {
	close(w.stopCh)
	select {
	case <-w.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) loop(ctx context.Context) {
	defer w.wg.Done()
	defer close(w.doneCh)
	tick := time.NewTicker(w.cfg.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-tick.C:
			w.drain(ctx)
		}
	}
}

func (w *Worker) drain(ctx context.Context) {
	deliveries, err := w.cfg.DeliveryStore.Claim(ctx, w.cfg.BatchSize)
	if err != nil {
		w.cfg.Logger.Error("webhooks: claim failed", "err", err)
		return
	}
	if len(deliveries) == 0 {
		return
	}
	sem := make(chan struct{}, w.cfg.MaxInFlight)
	var wg sync.WaitGroup
	for _, d := range deliveries {
		d := d
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					w.cfg.Logger.Warn("webhooks: attempt panic recovered",
						"delivery_id", d.ID, "subscription_id", d.SubscriptionID,
						"panic", fmt.Sprint(r))
					w.fail(ctx, d, 0, fmt.Sprintf("panic: %v", r))
				}
			}()
			w.attempt(ctx, d)
		}()
	}
	wg.Wait()
}

func (w *Worker) attempt(ctx context.Context, d Delivery) {
	start := time.Now()
	w.metrics.inFlightInc()
	defer w.metrics.inFlightDec()

	// Per-subscription circuit breaker — if open, treat as retryable
	// so the delivery reschedules without burning the in-flight slot
	// on a known-down endpoint.
	br := w.breakerFor(d.SubscriptionID)
	var brDone func(success bool)
	if br != nil {
		allowed, done := br.Allow()
		if !allowed {
			w.cfg.Logger.Warn("webhooks: breaker open, rescheduling",
				"delivery_id", d.ID, "subscription_id", d.SubscriptionID)
			w.fireOnAttempt(d, nil, breaker.ErrOpen, OutcomeRetryable, time.Since(start))
			w.fail(ctx, d, 0, "circuit open")
			return
		}
		brDone = done
	}
	// Helper: report (resp, err) outcome to the breaker once we know
	// the verdict. Safe to call on nil brDone.
	markBreaker := func(outcome Outcome) {
		if brDone != nil {
			brDone(outcome == OutcomeDelivered)
			brDone = nil
		}
	}
	defer func() {
		// Final safety net — if attempt() panics or returns early
		// without setting brDone to nil, charge a failure so the
		// breaker doesn't leak a probe slot.
		if brDone != nil {
			brDone(false)
		}
	}()

	sig, err := w.sign(d.Payload, d.Secret, start)
	if err != nil {
		w.fireOnAttempt(d, nil, err, OutcomeRetryable, time.Since(start))
		markBreaker(OutcomeRetryable)
		w.fail(ctx, d, 0, err.Error())
		return
	}

	attemptCtx, cancel := context.WithTimeout(ctx, w.cfg.AttemptTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, "POST", d.TargetURL, bytes.NewReader(d.Payload))
	if err != nil {
		w.fireOnAttempt(d, nil, err, OutcomeFatal, time.Since(start))
		markBreaker(OutcomeFatal)
		w.fail(ctx, d, 0, err.Error())
		return
	}
	req.Header.Set("Content-Type", w.cfg.DefaultContentType)
	req.Header.Set(SignatureHeader, sig)
	req.Header.Set("X-Webhook-Delivery", d.ID.String())
	req.Header.Set("X-Webhook-Event-Type", d.EventType)
	for k, vs := range d.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	// W3C TraceContext propagation — same shape as clients/nats
	// publishBytes so consumers extract continuations the same way.
	if w.cfg.Propagator != nil {
		w.cfg.Propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))
	}

	resp, err := w.cfg.HTTPClient.Do(req)
	elapsed := time.Since(start)
	w.metrics.deliveryDuration(elapsed)
	outcome := w.classify(resp, err)
	w.fireOnAttempt(d, resp, err, outcome, elapsed)
	markBreaker(outcome)

	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		w.fail(ctx, d, 0, err.Error())
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))

	switch outcome {
	case OutcomeDelivered:
		_ = w.cfg.DeliveryStore.MarkDelivered(ctx, d.ID, resp.StatusCode)
		w.metrics.attempted("delivered")
	case OutcomeRetryable:
		w.fail(ctx, d, resp.StatusCode, fmt.Sprintf("status %d", resp.StatusCode))
	case OutcomeFatal:
		_ = w.cfg.DeliveryStore.MarkDLQ(ctx, d.ID, resp.StatusCode,
			fmt.Sprintf("fatal status %d", resp.StatusCode))
		w.metrics.attempted("fatal")
		w.fireOnDLQ(d, resp.StatusCode, fmt.Sprintf("fatal status %d", resp.StatusCode))
	}
}

// fireOnAttempt invokes the configured OnAttempt hook, recovering
// from user-callback panics.
func (w *Worker) fireOnAttempt(d Delivery, resp *http.Response, err error, outcome Outcome, elapsed time.Duration) {
	if w.cfg.OnAttempt == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			w.cfg.Logger.Warn("webhooks: OnAttempt panic recovered",
				"delivery_id", d.ID, "panic", fmt.Sprint(r))
		}
	}()
	w.cfg.OnAttempt(d, resp, err, outcome, elapsed)
}

// fireOnDLQ invokes the configured OnDLQ hook, recovering from
// user-callback panics.
func (w *Worker) fireOnDLQ(d Delivery, statusCode int, errMsg string) {
	if w.cfg.OnDLQ == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			w.cfg.Logger.Warn("webhooks: OnDLQ panic recovered",
				"delivery_id", d.ID, "panic", fmt.Sprint(r))
		}
	}()
	w.cfg.OnDLQ(d, statusCode, errMsg)
}

func (w *Worker) fail(ctx context.Context, d Delivery, status int, msg string) {
	attempts := d.Attempts + 1
	if attempts >= w.cfg.MaxAttempts {
		_ = w.cfg.DeliveryStore.MarkDLQ(ctx, d.ID, status, msg)
		w.cfg.Logger.Error("webhooks: delivery to DLQ",
			"delivery_id", d.ID, "subscription_id", d.SubscriptionID,
			"attempts", attempts, "last_status", status, "last_error", msg)
		w.metrics.attempted("fatal")
		w.fireOnDLQ(d, status, msg)
		return
	}
	next := time.Now().Add(w.cfg.Backoff.attemptDelay(attempts))
	_ = w.cfg.DeliveryStore.MarkFailed(ctx, d.ID, status, msg, next)
	w.metrics.attempted("retryable")
}
