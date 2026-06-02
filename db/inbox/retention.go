package inbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// RetentionConfig configures [NewRetentionWorker]. Both TTL and
// Interval are required — inbox rows are receipts, and there is no
// universally-good default for "how long to keep them" (compliance
// shops want years; high-throughput consumers want hours).
type RetentionConfig struct {
	// TTL is the row age threshold. On each tick the worker DELETEs
	// rows where processed_at < NOW() - TTL.
	TTL time.Duration

	// Interval is the sweep cadence. A short interval means smaller
	// per-tick DELETE batches but more wake-ups; a long interval the
	// opposite. Typical: TTL/24 ≤ Interval ≤ TTL.
	Interval time.Duration

	// Logger receives Info on each tick (rows_deleted, duration_ms)
	// and Warn on failures. nil = silent.
	Logger *slog.Logger

	// Metrics, when non-nil, registers
	// inbox_retention_rows_deleted_total,
	// inbox_retention_tick_duration_seconds, and
	// inbox_retention_tick_errors_total on reg.
	Metrics prometheus.Registerer
}

// RetentionWorker periodically prunes the inbox table. Multi-replica
// deployments running this worker on every node will all DELETE — PG
// handles the concurrent DELETEs correctly but the duplicated work
// is wasted. Use [db/lock] yourself to pick one leader per cluster,
// or just run the worker on a single instance (the typical "one
// admin pod" pattern).
type RetentionWorker struct {
	db        *db.DB
	cfg       RetentionConfig
	exit      chan struct{}
	stopped   bool
	stopMu    sync.Mutex
	collector *retentionCollector
}

// NewRetentionWorker validates cfg and constructs a worker. Call
// [RetentionWorker.Start] to begin sweeping.
func NewRetentionWorker(d *db.DB, cfg RetentionConfig) (*RetentionWorker, error) {
	if cfg.TTL <= 0 {
		return nil, xerrs.Validation(CodeInvalidRetentionTTL,
			"inbox: RetentionConfig.TTL must be > 0")
	}
	if cfg.Interval <= 0 {
		return nil, xerrs.Validation(CodeInvalidRetentionInterval,
			"inbox: RetentionConfig.Interval must be > 0")
	}
	w := &RetentionWorker{
		db:   d,
		cfg:  cfg,
		exit: make(chan struct{}),
	}
	if cfg.Metrics != nil {
		w.collector = newRetentionCollector(cfg.Metrics)
	}
	return w, nil
}

// Start launches the sweep loop in a goroutine. The loop exits when
// ctx is cancelled OR [Stop] is called. The first sweep runs
// immediately — a long-running redeploy with a stale inbox does not
// have to wait one full Interval before any pruning happens.
func (w *RetentionWorker) Start(ctx context.Context) {
	go w.loop(ctx)
}

// Stop signals the loop to exit and blocks until it does. Idempotent:
// safe to call multiple times; returns immediately on the second call.
func (w *RetentionWorker) Stop() {
	w.stopMu.Lock()
	if w.stopped {
		w.stopMu.Unlock()
		return
	}
	w.stopped = true
	close(w.exit)
	w.stopMu.Unlock()
}

// Tick performs one prune synchronously and returns the row count.
// Useful for one-shot maintenance commands AND tests. Independent of
// the loop — calling Tick after Stop is fine.
func (w *RetentionWorker) Tick(ctx context.Context) (int64, error) {
	return w.runTick(ctx)
}

func (w *RetentionWorker) loop(ctx context.Context) {
	if _, err := w.runTick(ctx); err != nil && !errors.Is(err, context.Canceled) {
		// runTick already logged; the loop keeps going.
		_ = err
	}
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.exit:
			return
		case <-ticker.C:
			if _, err := w.runTick(ctx); err != nil && !errors.Is(err, context.Canceled) {
				_ = err
			}
		}
	}
}

func (w *RetentionWorker) runTick(ctx context.Context) (int64, error) {
	const sql = `DELETE FROM inbox WHERE processed_at < NOW() - $1::interval`
	interval := fmt.Sprintf("%d microseconds", w.cfg.TTL.Microseconds())
	start := time.Now()
	tag, err := w.db.Exec(ctx, sql, interval)
	dur := time.Since(start)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 0, err
		}
		w.collector.observeError(dur)
		if w.cfg.Logger != nil {
			w.cfg.Logger.WarnContext(ctx, "inbox retention tick failed",
				"err", err.Error(),
				"duration_ms", dur.Milliseconds())
		}
		return 0, xerrs.Wrap(err, xerrs.KindInternal, CodeRetentionTickFailed,
			"inbox: retention tick: delete failed")
	}
	rows := tag.RowsAffected()
	w.collector.observeOK(rows, dur)
	if w.cfg.Logger != nil {
		w.cfg.Logger.InfoContext(ctx, "inbox retention tick",
			"rows_deleted", rows,
			"duration_ms", dur.Milliseconds())
	}
	return rows, nil
}
