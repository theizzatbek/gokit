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

	"github.com/prometheus/client_golang/prometheus"
)

type attemptOutcome int

const (
	outcomeDelivered attemptOutcome = iota
	outcomeRetryable
	outcomeFatal
)

func classify(statusCode int) attemptOutcome {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return outcomeDelivered
	case statusCode == 408 || statusCode == 429 || (statusCode >= 500 && statusCode < 600):
		return outcomeRetryable
	default:
		return outcomeFatal
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
}

// Worker drains pending deliveries and dispatches them.
type Worker struct {
	cfg     WorkerConfig
	signer  *Signer
	stopCh  chan struct{}
	doneCh  chan struct{}
	wg      sync.WaitGroup
	metrics *workerMetrics
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
		cfg.Interval = time.Second
	}
	if cfg.MaxInFlight == 0 {
		cfg.MaxInFlight = 16
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
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
			w.attempt(ctx, d)
		}()
	}
	wg.Wait()
}

func (w *Worker) attempt(ctx context.Context, d Delivery) {
	start := time.Now()
	w.metrics.inFlightInc()
	defer w.metrics.inFlightDec()

	sig, err := w.signer.Sign(d.Payload, d.Secret, start)
	if err != nil {
		w.fail(ctx, d, 0, err.Error())
		return
	}

	attemptCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, "POST", d.TargetURL, bytes.NewReader(d.Payload))
	if err != nil {
		w.fail(ctx, d, 0, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SignatureHeader, sig)
	req.Header.Set("X-Webhook-Delivery", d.ID.String())
	req.Header.Set("X-Webhook-Event-Type", d.EventType)
	for k, vs := range d.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := w.cfg.HTTPClient.Do(req)
	w.metrics.deliveryDuration(time.Since(start))
	if err != nil {
		w.fail(ctx, d, 0, err.Error())
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))

	switch classify(resp.StatusCode) {
	case outcomeDelivered:
		_ = w.cfg.DeliveryStore.MarkDelivered(ctx, d.ID, resp.StatusCode)
		w.metrics.attempted("delivered")
	case outcomeRetryable:
		w.fail(ctx, d, resp.StatusCode, fmt.Sprintf("status %d", resp.StatusCode))
	case outcomeFatal:
		_ = w.cfg.DeliveryStore.MarkDLQ(ctx, d.ID, resp.StatusCode,
			fmt.Sprintf("fatal status %d", resp.StatusCode))
		w.metrics.attempted("fatal")
	}
}

func (w *Worker) fail(ctx context.Context, d Delivery, status int, msg string) {
	attempts := d.Attempts + 1
	if attempts >= w.cfg.MaxAttempts {
		_ = w.cfg.DeliveryStore.MarkDLQ(ctx, d.ID, status, msg)
		w.cfg.Logger.Error("webhooks: delivery to DLQ",
			"delivery_id", d.ID, "subscription_id", d.SubscriptionID,
			"attempts", attempts, "last_status", status, "last_error", msg)
		w.metrics.attempted("fatal")
		return
	}
	next := time.Now().Add(w.cfg.Backoff.attemptDelay(attempts))
	_ = w.cfg.DeliveryStore.MarkFailed(ctx, d.ID, status, msg, next)
	w.metrics.attempted("retryable")
}
