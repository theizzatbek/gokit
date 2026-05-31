package batch

import (
	"context"
	"errors"
	"log/slog"
	"sync"
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
	handlerFn func(context.Context, []T) error
	interval  time.Duration
	batchSize int
	logger    *slog.Logger
	metrics   *metricsCollector

	mu      sync.Mutex
	pending []submission[T]

	flushCh  chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
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

	b := &Batcher[T]{
		handlerFn: cfg.HandlerFn,
		interval:  cfg.Interval,
		batchSize: cfg.BatchSize,
		logger:    cfg.Logger,
		pending:   make([]submission[T], 0, cfg.BatchSize),
		flushCh:   make(chan struct{}, 1),
		doneCh:    make(chan struct{}),
	}
	if cfg.Metrics != nil {
		b.metrics = newMetricsCollector(cfg.Metrics)
	}

	b.wg.Add(1)
	go b.flushLoop()
	return b, nil
}

func validate[T any](cfg Config[T]) error {
	var errs []error
	if cfg.HandlerFn == nil {
		errs = append(errs, xerrs.Validation(CodeMissingHandlerFn, "batch: Config.HandlerFn is required"))
	}
	if cfg.BatchSize <= 0 {
		errs = append(errs, xerrs.Validationf(CodeInvalidBatchSize, "batch: Config.BatchSize must be > 0 (got %d)", cfg.BatchSize))
	}
	return errors.Join(errs...)
}

// Submit appends an item to the buffer with an optional ack
// callback. The callback fires AFTER the batch's HandlerFn returns,
// with that handler's error (nil on success). nil ack is supported
// for fire-and-forget items.
//
// When the buffer hits BatchSize a non-blocking flush signal jumps
// the goroutine's queue.
//
// Nil-receiver safe.
func (b *Batcher[T]) Submit(item T, ack func(err error)) {
	if b == nil {
		return
	}
	b.mu.Lock()
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
			// A flush is already pending; the buffered slot will
			// pick this batch up on the next loop iteration.
		}
	}
}

// Flush atomically swaps the pending buffer and dispatches it. Use
// to force a drain from a test or a graceful shutdown path. Returns
// the HandlerFn's return value; nil-receiver returns nil.
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
	return b.dispatch(ctx, batch)
}

// dispatch slices the items, calls HandlerFn outside the lock, then
// fires every submission's ack with the handler's return value.
func (b *Batcher[T]) dispatch(ctx context.Context, batch []submission[T]) error {
	items := make([]T, len(batch))
	for i, s := range batch {
		items[i] = s.item
	}

	start := time.Now()
	err := b.handlerFn(ctx, items)
	elapsed := time.Since(start)

	if b.metrics != nil {
		b.metrics.observeHandle(err, elapsed, len(batch))
	}
	if err != nil && b.logger != nil {
		b.logger.Warn("batch: handler returned error",
			"items", len(batch), "err", err.Error())
	}
	for _, s := range batch {
		if s.ack != nil {
			s.ack(err)
		}
	}
	return err
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
	return nil
}

// flushLoop is the single goroutine that drains the batcher on
// ticker / size-trigger / shutdown signals. Survives HandlerFn
// errors — they're observed (metrics + log + ack callbacks) and
// the loop continues.
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
