package batch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	xerrs "github.com/theizzatbek/gokit/errs"
)

const defaultInterval = time.Second

// Batcher dispatches buffered items to a HandlerFn in fixed-size or
// time-bounded slices. Construct via [New]. Goroutine-safe — Submit
// may be called from any number of producer goroutines concurrently
// with Flush/Close.
//
// A nil *Batcher is safe on Submit/Flush/Close — Submit is a no-op
// and the closing methods return nil. Lets callers thread an
// optional batcher through their code unconditionally.
type Batcher[T any] struct {
	handlerFn   func(context.Context, []T) error
	interval    time.Duration
	batchSize   int
	maxPending  int
	maxInFlight int
	maxRetries  int
	retryBase   time.Duration
	retryMax    time.Duration
	isRetryable func(err error) bool
	contextFn   func() context.Context
	onStart     func(ctx context.Context, size int)
	onComplete  func(ctx context.Context, size int, err error, elapsed time.Duration)
	logger      *slog.Logger
	metrics     *metricsCollector

	mu      sync.Mutex
	pending []submission[T]

	// Counters for Stats(). Read+write via atomic so Stats stays
	// lock-free with respect to dispatch.
	inFlightHandlers atomic.Int64
	dispatchedTotal  atomic.Int64
	failedHandlers   atomic.Int64
	retriedAttempts  atomic.Int64

	// In-flight handler concurrency cap (semaphore).
	dispatchSlots chan struct{}

	flushCh    chan struct{}
	doneCh     chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
	dispatchWG sync.WaitGroup // running dispatch goroutines when maxInFlight > 1
}

// submission pairs an item with its per-item ack callback. ack may
// be nil for fire-and-forget items; the flush dispatcher checks for
// nil before calling.
type submission[T any] struct {
	item T
	ack  func(error)
}

// New validates cfg, applies defaults, starts the flush goroutine,
// and returns the batcher. Missing required fields return an
// errors.Join of every *errs.Error so a misconfigured Config
// surfaces the whole truth at once.
func New[T any](cfg Config[T]) (*Batcher[T], error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultInterval
	}
	if cfg.MaxInFlightHandlers <= 0 {
		cfg.MaxInFlightHandlers = 1
	}

	isRetryable := cfg.IsRetryable
	if isRetryable == nil {
		isRetryable = defaultIsRetryable
	}

	b := &Batcher[T]{
		handlerFn:     cfg.HandlerFn,
		interval:      cfg.Interval,
		batchSize:     cfg.BatchSize,
		maxPending:    cfg.MaxPending,
		maxInFlight:   cfg.MaxInFlightHandlers,
		maxRetries:    cfg.MaxRetries,
		retryBase:     cfg.RetryBackoffBase,
		retryMax:      cfg.RetryBackoffMax,
		isRetryable:   isRetryable,
		contextFn:     cfg.ContextFn,
		onStart:       cfg.OnBatchStart,
		onComplete:    cfg.OnBatchComplete,
		logger:        cfg.Logger,
		pending:       make([]submission[T], 0, cfg.BatchSize),
		dispatchSlots: make(chan struct{}, cfg.MaxInFlightHandlers),
		flushCh:       make(chan struct{}, 1),
		doneCh:        make(chan struct{}),
	}
	if cfg.Metrics != nil {
		b.metrics = newMetricsCollector(cfg.Metrics)
	}

	b.wg.Add(1)
	go b.flushLoop()
	return b, nil
}

func validate[T any](cfg Config[T]) error {
	var errsAcc []error
	if cfg.HandlerFn == nil {
		errsAcc = append(errsAcc, xerrs.Validation(CodeMissingHandlerFn, "batch: Config.HandlerFn is required"))
	}
	if cfg.BatchSize <= 0 {
		errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidBatchSize, "batch: Config.BatchSize must be > 0 (got %d)", cfg.BatchSize))
	}
	if cfg.MaxPending < 0 {
		errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidConfig, "batch: Config.MaxPending must be >= 0 (got %d; 0 = unbounded)", cfg.MaxPending))
	}
	if cfg.MaxInFlightHandlers < 0 {
		errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidConfig, "batch: Config.MaxInFlightHandlers must be >= 0 (0 applies default 1)"))
	}
	if cfg.MaxRetries < 0 {
		errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidConfig, "batch: Config.MaxRetries must be >= 0"))
	}
	if cfg.RetryBackoffBase < 0 || cfg.RetryBackoffMax < 0 {
		errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidConfig, "batch: Config.RetryBackoff* must be >= 0"))
	}
	return errors.Join(errsAcc...)
}

// Submit appends an item to the buffer with an optional ack
// callback. The callback fires AFTER the batch's HandlerFn returns,
// with that handler's error (nil on success). nil ack is supported
// for fire-and-forget items.
//
// When the buffer hits BatchSize a non-blocking flush signal jumps
// the goroutine's queue.
//
// Backpressure: when [Config.MaxPending] > 0 and the pending buffer
// reached the cap, Submit silently drops the item AND immediately
// calls ack(ErrPendingFull). Callers needing synchronous notification
// of backpressure should use [Batcher.TrySubmit].
//
// Nil-receiver safe.
func (b *Batcher[T]) Submit(item T, ack func(err error)) {
	if b == nil {
		return
	}
	if err := b.submitOrDrop(item, ack); err != nil && ack != nil {
		ack(err)
	}
}

// TrySubmit is the error-returning variant of Submit. Returns
// [ErrPendingFull] when [Config.MaxPending] > 0 and the buffer is at
// the cap. The supplied ack is still called normally on the dispatch
// path; TrySubmit's return is the immediate backpressure signal.
//
// Nil-receiver returns nil so callers can wire an optional batcher.
func (b *Batcher[T]) TrySubmit(item T, ack func(err error)) error {
	if b == nil {
		return nil
	}
	return b.submitOrDrop(item, ack)
}

// submitOrDrop is the shared path. Returns ErrPendingFull when the
// MaxPending cap is reached; the item is NOT appended in that case.
func (b *Batcher[T]) submitOrDrop(item T, ack func(error)) error {
	b.mu.Lock()
	if b.maxPending > 0 && len(b.pending) >= b.maxPending {
		b.mu.Unlock()
		return ErrPendingFull
	}
	b.pending = append(b.pending, submission[T]{item: item, ack: ack})
	full := len(b.pending) >= b.batchSize
	b.mu.Unlock()

	if b.metrics != nil {
		b.metrics.incItems()
	}
	if full {
		select {
		case b.flushCh <- struct{}{}:
		default:
		}
	}
	return nil
}

// Flush atomically swaps the pending buffer and dispatches it.
//
// With MaxInFlightHandlers == 1 (default — back-compat), Flush runs
// the dispatch synchronously and returns the HandlerFn's error.
//
// With MaxInFlightHandlers > 1, Flush spawns the dispatch into a
// goroutine (so the flushLoop can keep accepting new size triggers
// while a slow batch is in flight) and returns nil. The handler's
// outcome surfaces via the per-item ack callbacks and the metrics /
// Stats counters.
func (b *Batcher[T]) Flush(ctx context.Context) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if len(b.pending) == 0 {
		b.mu.Unlock()
		return nil
	}
	batch := b.pending
	b.pending = make([]submission[T], 0, b.batchSize)
	b.mu.Unlock()

	if b.maxInFlight == 1 {
		return b.dispatch(ctx, batch)
	}
	b.dispatchWG.Add(1)
	go func() {
		defer b.dispatchWG.Done()
		_ = b.dispatch(ctx, batch)
	}()
	return nil
}

// dispatch slices the items, runs the configured retry loop with
// panic recovery, then fires every submission's ack with the final
// outcome. inFlight is bounded by the dispatchSlots semaphore.
func (b *Batcher[T]) dispatch(ctx context.Context, batch []submission[T]) error {
	b.dispatchSlots <- struct{}{}
	defer func() { <-b.dispatchSlots }()

	b.inFlightHandlers.Add(1)
	defer b.inFlightHandlers.Add(-1)

	items := make([]T, len(batch))
	for i, s := range batch {
		items[i] = s.item
	}

	// Resolve dispatch ctx: caller-supplied (Flush(ctx)) wins when
	// non-nil + non-Background; otherwise use the Batcher's contextFn
	// (typically tracing-aware) or fall back to context.Background.
	dispatchCtx := ctx
	if b.contextFn != nil && (dispatchCtx == nil || dispatchCtx == context.Background()) {
		dispatchCtx = b.contextFn()
	}
	if dispatchCtx == nil {
		dispatchCtx = context.Background()
	}

	if b.onStart != nil {
		b.fireOnStart(dispatchCtx, len(batch))
	}

	start := time.Now()
	var err error
retryLoop:
	for attempt := 0; attempt <= b.maxRetries; attempt++ {
		if attempt > 0 {
			b.retriedAttempts.Add(1)
			wait := b.retryDelay(attempt)
			select {
			case <-dispatchCtx.Done():
				// Dispatch ctx cancelled mid-backoff: surface the
				// cancellation and bail. Labeled break ensures we
				// exit the for-loop entirely instead of falling
				// through to another runHandlerSafely call (a bare
				// `break` here would only leave the select).
				err = dispatchCtx.Err()
				break retryLoop
			case <-time.After(wait):
			}
		}
		err = b.runHandlerSafely(dispatchCtx, items)
		if err == nil {
			break
		}
		// Classifier breaks the retry budget early when the
		// caller (or the default) considers err permanent. The
		// default treats ctx.Canceled / ctx.DeadlineExceeded as
		// non-retryable so a cancelled dispatch surfaces on the
		// first failed attempt rather than burning the budget.
		if !b.isRetryable(err) {
			break
		}
	}
	elapsed := time.Since(start)

	if b.metrics != nil {
		b.metrics.observeHandle(err, elapsed, len(batch))
	}
	if err != nil {
		b.failedHandlers.Add(1)
		if b.logger != nil {
			b.logger.Warn("batch: handler returned error",
				"items", len(batch), "err", err.Error())
		}
	}
	b.dispatchedTotal.Add(int64(len(batch)))

	if b.onComplete != nil {
		b.fireOnComplete(dispatchCtx, len(batch), err, elapsed)
	}

	for _, s := range batch {
		if s.ack != nil {
			s.ack(err)
		}
	}
	return err
}

// runHandlerSafely invokes HandlerFn under recover() so a panic does
// NOT take down the flushLoop goroutine. The recovered panic surfaces
// as a regular error so the retry loop + ack callbacks treat it
// uniformly.
func (b *Batcher[T]) runHandlerSafely(ctx context.Context, items []T) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("batch: handler panic: %v", r)
			if b.logger != nil {
				b.logger.Warn("batch: handler panic recovered",
					"items", len(items), "panic", fmt.Sprint(r))
			}
		}
	}()
	return b.handlerFn(ctx, items)
}

// defaultIsRetryable is the fallback classifier used when
// Config.IsRetryable is nil. It treats ctx-cancellation
// (context.Canceled / context.DeadlineExceeded) as permanent and
// every other error as transient — the right default for a batch
// pipeline whose dispatchCtx is the caller's signal to stop
// working.
func defaultIsRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// retryDelay returns the exponential backoff delay before attempt N
// (1-indexed). When RetryBackoffBase is zero, the delay is 0.
func (b *Batcher[T]) retryDelay(attempt int) time.Duration {
	if b.retryBase <= 0 {
		return 0
	}
	d := b.retryBase << (attempt - 1)
	if d <= 0 || (b.retryMax > 0 && d > b.retryMax) {
		return b.retryMax
	}
	return d
}

// fireOnStart invokes the OnBatchStart hook with panic recovery.
func (b *Batcher[T]) fireOnStart(ctx context.Context, size int) {
	defer func() {
		if r := recover(); r != nil && b.logger != nil {
			b.logger.Warn("batch: OnBatchStart panic recovered",
				"panic", fmt.Sprint(r))
		}
	}()
	b.onStart(ctx, size)
}

// fireOnComplete invokes the OnBatchComplete hook with panic recovery.
func (b *Batcher[T]) fireOnComplete(ctx context.Context, size int, err error, elapsed time.Duration) {
	defer func() {
		if r := recover(); r != nil && b.logger != nil {
			b.logger.Warn("batch: OnBatchComplete panic recovered",
				"panic", fmt.Sprint(r))
		}
	}()
	b.onComplete(ctx, size, err, elapsed)
}

// Stats is the cheap snapshot returned by [Batcher.Stats] —
// suitable for /admin or /healthz endpoints.
type Stats struct {
	// Pending is the number of items currently buffered.
	Pending int

	// InFlightHandlers is the count of dispatch goroutines that have
	// claimed a slot but not yet returned. Caps at
	// Config.MaxInFlightHandlers.
	InFlightHandlers int64

	// DispatchedTotal is the number of items handed to HandlerFn
	// over the batcher's lifetime (sum across all dispatches,
	// including retries beyond the first attempt).
	DispatchedTotal int64

	// FailedHandlers is the number of HandlerFn calls that finished
	// with a non-nil error after exhausting retries.
	FailedHandlers int64

	// RetriedAttempts is the number of retry attempts performed
	// (i.e. attempts beyond the first). Compare with FailedHandlers
	// to gauge how often retries succeeded.
	RetriedAttempts int64
}

// Stats returns the cheap point-in-time snapshot of the batcher.
// Nil receiver returns the zero value.
func (b *Batcher[T]) Stats() Stats {
	if b == nil {
		return Stats{}
	}
	b.mu.Lock()
	pending := len(b.pending)
	b.mu.Unlock()
	return Stats{
		Pending:          pending,
		InFlightHandlers: b.inFlightHandlers.Load(),
		DispatchedTotal:  b.dispatchedTotal.Load(),
		FailedHandlers:   b.failedHandlers.Load(),
		RetriedAttempts:  b.retriedAttempts.Load(),
	}
}

// Close stops the flush goroutine after one final drain.
// Idempotent — the second call is a no-op via sync.Once. Returns
// nil; the final flush's error is logged + metric'd + propagated
// via each item's ack callback, but not bubbled out.
//
// Nil-receiver safe.
func (b *Batcher[T]) Close() error {
	if b == nil {
		return nil
	}
	b.stopOnce.Do(func() { close(b.doneCh) })
	b.wg.Wait()
	// Wait for any async dispatch goroutines spawned by Flush when
	// MaxInFlightHandlers > 1.
	b.dispatchWG.Wait()
	return nil
}

// flushLoop is the single goroutine that drains the batcher on
// ticker / size-trigger / shutdown signals. Survives HandlerFn
// errors AND panics — both are observed (metrics + log + ack
// callbacks) and the loop continues.
func (b *Batcher[T]) flushLoop() {
	defer b.wg.Done()
	t := time.NewTicker(b.interval)
	defer t.Stop()
	for {
		select {
		case <-b.doneCh:
			_ = b.Flush(context.Background())
			return
		case <-t.C:
			_ = b.Flush(context.Background())
		case <-b.flushCh:
			_ = b.Flush(context.Background())
		}
	}
}
