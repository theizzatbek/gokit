package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/db"
	"github.com/theizzatbek/gokit/errs"
)

// PublishFn dispatches one Event to the real bus. Returning nil
// marks the row published; returning a non-nil error bumps attempts
// and records the error string on the row — the Worker retries the
// event on the next tick.
//
// Implementations should be idempotent at the bus level (e.g. set
// the NATS `Nats-Msg-Id` header to the Event.ID) since at-least-once
// delivery is the contract. PublishFn receives the surrounding
// fetch-batch's context; honour ctx.Done() and return promptly so
// shutdown isn't blocked.
type PublishFn func(ctx context.Context, e Event) error

// Stable error Code constants for Worker-level errors.
const (
	// CodeWorkerStarted — Start called twice. Worker is single-use.
	CodeWorkerStarted = "outbox_worker_started"

	// CodeWorkerNilPublishFn — Worker built without a PublishFn.
	// Refused at NewWorker rather than panicking at first tick.
	CodeWorkerNilPublishFn = "outbox_worker_nil_publish_fn"

	// CodeWorkerNilDB — Worker built with nil *db.DB. Refused at
	// NewWorker.
	CodeWorkerNilDB = "outbox_worker_nil_db"
)

// WorkerOption tunes [NewWorker].
type WorkerOption func(*Worker)

// WithInterval sets the polling cadence. Default 500ms — drains
// new events within sub-second latency at negligible DB load
// (one indexed SELECT per tick). Raise for very-large deployments
// where DB pressure matters more than publish latency.
func WithInterval(d time.Duration) WorkerOption {
	return func(w *Worker) { w.interval = d }
}

// WithBatchSize caps the number of events fetched per tick.
// Default 100. Larger values amortise the round-trip but hold the
// row locks longer (FOR UPDATE SKIP LOCKED keeps the locks scoped
// to the fetched batch).
func WithBatchSize(n int) WorkerOption {
	return func(w *Worker) {
		if n > 0 {
			w.batchSize = n
		}
	}
}

// WithMaxAttempts dead-letters events whose attempt count would
// exceed n on the NEXT failure. After dead-lettering, the row's
// `published_at` is left NULL but the Worker no longer picks it up
// — operators must manually inspect / replay / delete. Default 0
// (no cap — events retry forever until they succeed).
//
// "Dead-lettering" here is a SELECT-side filter only — the row
// stays in the table. No automatic deletion / move to a separate
// table; operators decide the disposition.
func WithMaxAttempts(n int) WorkerOption {
	return func(w *Worker) {
		if n > 0 {
			w.maxAttempts = n
		}
	}
}

// WithRetention enables periodic GC of published rows older than
// retention. The retention goroutine sweeps at gcInterval (default
// 1h) and emits `outbox_gc_deleted_total` when [WithMetrics] is
// also wired.
//
//	outbox.WithRetention(7 * 24 * time.Hour) // keep one week
//
// Default off — published rows live forever until an operator
// manually cleans them up. Useful when downstream replay tooling
// reaches into the outbox table for audit trails.
//
// retention ≤ 0 disables GC even after this option is passed
// (consistent with the other WithX-with-duration options).
func WithRetention(retention time.Duration) WorkerOption {
	return func(w *Worker) { w.retention = retention }
}

// WithGCInterval overrides the retention sweep cadence. Default
// 1h. Cheaper sweeps run more often; a very-long-retention
// deployment may want hourly even for very large tables. No-op
// without [WithRetention].
func WithGCInterval(d time.Duration) WorkerOption {
	return func(w *Worker) {
		if d > 0 {
			w.gcInterval = d
		}
	}
}

// WithBackoff configures per-row exponential retry timing. After a
// failed PublishFn, the worker stamps
// `next_retry_at = NOW() + base * 2^(attempts-1)` (capped at max),
// so a stuck event backs off instead of hammering the bus every
// polling tick. Defaults: base 1s, max 1h.
//
// Set base ≤ 0 to disable backoff entirely — failed rows become
// eligible on the next tick regardless of attempt count. Useful in
// tests that drive the worker through artificial failures; should
// stay disabled in production.
func WithBackoff(base, max time.Duration) WorkerOption {
	return func(w *Worker) {
		w.backoffBase = base
		w.backoffMax = max
	}
}

// BackoffSpec is the per-event-type backoff override carried by
// [WithEventTypeBackoff]. Same semantics as [WithBackoff] — Base
// is the initial wait, Max is the cap.
type BackoffSpec struct {
	Base time.Duration
	Max  time.Duration
}

// WithEventTypeMaxAttempts overrides [WithMaxAttempts] for specific
// event types. The Worker still uses the global maxAttempts as the
// default; only listed types get the override.
//
//	outbox.WithEventTypeMaxAttempts(map[string]int{
//	    "billing.charge": 10,   // more headroom for flaky upstream
//	    "audit.log":      1,    // best-effort, don't pile up
//	})
//
// When this option is set, the drain SELECT drops the global
// attempts filter — per-type filtering happens in the Go-side
// dispatch loop. Rows already at their per-type cap are silently
// skipped without invoking PublishFn (no failure counter ticks).
//
// Empty map is a no-op — same as not passing the option.
func WithEventTypeMaxAttempts(m map[string]int) WorkerOption {
	return func(w *Worker) {
		if len(m) == 0 {
			return
		}
		w.eventTypeMaxAttempts = make(map[string]int, len(m))
		for k, v := range m {
			w.eventTypeMaxAttempts[k] = v
		}
	}
}

// WithEventTypeBackoff overrides [WithBackoff] (Base, Max pair) for
// specific event types. Same shape as [WithEventTypeMaxAttempts].
//
//	outbox.WithEventTypeBackoff(map[string]outbox.BackoffSpec{
//	    "billing.charge": {Base: 5 * time.Second, Max: 10 * time.Minute},
//	    "notif.email":    {Base: 30 * time.Second, Max: time.Hour},
//	})
//
// Empty map is a no-op.
func WithEventTypeBackoff(m map[string]BackoffSpec) WorkerOption {
	return func(w *Worker) {
		if len(m) == 0 {
			return
		}
		w.eventTypeBackoff = make(map[string]BackoffSpec, len(m))
		for k, v := range m {
			w.eventTypeBackoff[k] = v
		}
	}
}

// WithLogger wires a slog.Logger for Worker lifecycle + per-event
// success / failure logs. Without it the Worker runs silently. Levels:
//   - Debug: every successful batch (count + duration).
//   - Warn:  individual PublishFn failures (event_id + error).
//   - Error: SELECT / UPDATE failures (the Worker can't drain).
func WithLogger(l *slog.Logger) WorkerOption {
	return func(w *Worker) { w.logger = l }
}

// WithMetrics registers Prometheus collectors on reg:
//   - outbox_events_total{outcome=success|failure|dead_letter|gc_deleted} (counter)
//   - outbox_publish_duration_seconds                                     (histogram)
//   - outbox_pending_count                                                (gauge)
//   - outbox_gc_deleted_total                                             (counter)
//
// Without this option no collectors are created (zero Prometheus
// footprint). Wire the unified service registry via
// `service.WithOutbox` so outbox metrics scrape from the same
// `/metrics` endpoint as the rest of the kit.
func WithMetrics(reg prometheus.Registerer) WorkerOption {
	return func(w *Worker) { w.metrics = newMetricsCollector(reg) }
}

// Worker drains the outbox table by polling. Built via [NewWorker];
// drive with [Worker.Start] and stop with [Worker.Stop].
type Worker struct {
	db          *db.DB
	publishFn   PublishFn
	interval    time.Duration
	batchSize   int
	maxAttempts int
	backoffBase time.Duration
	backoffMax  time.Duration
	retention   time.Duration
	gcInterval  time.Duration
	logger      *slog.Logger
	metrics     *metricsCollector

	// Per-event-type policy overrides. Read-only post-NewWorker; nil
	// when no overrides are configured (back-compat hot path).
	eventTypeMaxAttempts map[string]int
	eventTypeBackoff     map[string]BackoffSpec

	startOnce sync.Once
	stopOnce  sync.Once
	cancel    context.CancelFunc
	done      chan struct{}
	gcExit    chan struct{}
}

// maxAttemptsFor returns the effective maxAttempts for the event
// type — per-type override when set, else the global default.
func (w *Worker) maxAttemptsFor(eventType string) int {
	if w.eventTypeMaxAttempts != nil {
		if v, ok := w.eventTypeMaxAttempts[eventType]; ok {
			return v
		}
	}
	return w.maxAttempts
}

// backoffSpecFor returns the effective (base, max) backoff for the
// event type — per-type override when set, else the global default.
func (w *Worker) backoffSpecFor(eventType string) (time.Duration, time.Duration) {
	if w.eventTypeBackoff != nil {
		if v, ok := w.eventTypeBackoff[eventType]; ok {
			return v.Base, v.Max
		}
	}
	return w.backoffBase, w.backoffMax
}

const (
	defaultInterval    = 500 * time.Millisecond
	defaultBatchSize   = 100
	defaultBackoffBase = time.Second
	defaultBackoffMax  = time.Hour
	defaultGCInterval  = time.Hour
)

// NewWorker constructs a Worker. fn is mandatory — Worker refuses
// to build without it. d is the underlying *db.DB the Worker uses
// to poll / update the outbox table.
//
// The Worker does NOT start polling automatically. Call Start to
// kick off the loop; pair with Stop (or service.OnShutdown(w.Stop))
// for graceful teardown.
func NewWorker(d *db.DB, fn PublishFn, opts ...WorkerOption) (*Worker, error) {
	if d == nil {
		return nil, errs.Validation(CodeWorkerNilDB, "outbox: NewWorker requires non-nil *db.DB")
	}
	if fn == nil {
		return nil, errs.Validation(CodeWorkerNilPublishFn, "outbox: NewWorker requires non-nil PublishFn")
	}
	w := &Worker{
		db:          d,
		publishFn:   fn,
		interval:    defaultInterval,
		batchSize:   defaultBatchSize,
		backoffBase: defaultBackoffBase,
		backoffMax:  defaultBackoffMax,
		gcInterval:  defaultGCInterval,
		done:        make(chan struct{}),
	}
	for _, opt := range opts {
		opt(w)
	}
	return w, nil
}

// Start kicks off the polling goroutine (plus the retention sweeper
// when [WithRetention] is set). Idempotent — second call returns
// *errs.Error{Code: CodeWorkerStarted} without spawning new
// goroutines. The supplied ctx anchors the goroutine lifetimes —
// they exit when ctx is cancelled OR Stop is called.
//
// Start fires the first fetch immediately so events Enqueued just
// before Start land without waiting for the first tick.
func (w *Worker) Start(ctx context.Context) error {
	if w == nil {
		return nil
	}
	started := false
	w.startOnce.Do(func() {
		started = true
		loopCtx, cancel := context.WithCancel(ctx)
		w.cancel = cancel
		if w.retention > 0 {
			w.gcExit = make(chan struct{})
			go w.gcLoop(loopCtx)
		}
		go w.loop(loopCtx)
	})
	if !started {
		return errs.Validation(CodeWorkerStarted, "outbox: worker already started")
	}
	return nil
}

// Stop cancels the polling goroutine and waits for it to exit.
// Idempotent + nil-safe. Returns nil — the Worker's loop swallows
// errors per-tick (logged when WithLogger is set); a clean shutdown
// has no error to surface.
func (w *Worker) Stop() error {
	if w == nil {
		return nil
	}
	w.stopOnce.Do(func() {
		if w.cancel != nil {
			w.cancel()
		}
		<-w.done
		if w.gcExit != nil {
			<-w.gcExit
		}
	})
	return nil
}

// loop is the drain loop. Wakes up on:
//   - ticker at WithInterval cadence.
//   - ctx done (Stop).
func (w *Worker) loop(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	w.tick(ctx) // immediate first fetch
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *Worker) tick(ctx context.Context) {
	start := time.Now()
	count, err := w.drainBatch(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		if w.logger != nil {
			w.logger.Error("outbox: drain failed", "err", err)
		}
		return
	}
	if w.logger != nil && count > 0 {
		w.logger.Debug("outbox: batch drained",
			"published", count, "elapsed", time.Since(start))
	}
	// Refresh the pending-count gauge after each drain so dashboards
	// reflect the up-to-date depth. Cheap query (partial-index count)
	// — runs at WithInterval cadence, not on every event.
	w.metrics.refreshPending(ctx, w)
}

// drainBatch fetches up to batchSize unpublished rows under
// FOR UPDATE SKIP LOCKED and dispatches each to PublishFn. The
// per-batch transaction holds locks until the Worker has updated
// every row's published_at OR attempts/last_error — at which point
// the locks release. Returns the count of newly-published events.
//
// When per-event-type maxAttempts overrides are configured, the SQL
// filter on attempts is dropped — the per-type cap is enforced in
// the Go dispatch loop below. Otherwise, the global maxAttempts is
// passed to selectBatch so dead-letter rows never leave the DB.
func (w *Worker) drainBatch(ctx context.Context) (int, error) {
	// Drop the SQL-side attempts filter when per-type overrides exist;
	// the Go dispatch loop applies the per-type cap below.
	sqlMaxAttempts := w.maxAttempts
	if w.eventTypeMaxAttempts != nil {
		sqlMaxAttempts = 0
	}
	var published int
	err := w.db.Tx(ctx, func(tx *db.Tx) error {
		events, err := selectBatch(ctx, tx, w.batchSize, sqlMaxAttempts)
		if err != nil {
			return err
		}
		for _, e := range events {
			if err := ctx.Err(); err != nil {
				return err
			}
			// Per-type dead-letter check — silently skip rows already at
			// their cap so a noisy event type doesn't starve the rest
			// of the batch.
			effMax := w.maxAttemptsFor(e.EventType)
			if effMax > 0 && e.Attempts >= effMax {
				continue
			}
			publishStart := time.Now()
			perr := w.publishFn(ctx, e)
			w.metrics.observePublish(time.Since(publishStart))
			if perr != nil {
				if w.logger != nil {
					w.logger.Warn("outbox: publish failed",
						"event_id", e.ID, "event_type", e.EventType,
						"attempts", e.Attempts+1, "err", perr.Error())
				}
				base, max := w.backoffSpecFor(e.EventType)
				retryAfter := backoffFor(e.Attempts+1, base, max)
				if uerr := markFailed(ctx, tx, e.ID, perr.Error(), retryAfter); uerr != nil {
					return uerr
				}
				if effMax > 0 && e.Attempts+1 >= effMax {
					w.metrics.recordOutcome(outcomeDeadLetter)
				} else {
					w.metrics.recordOutcome(outcomeFailure)
				}
				continue
			}
			if uerr := markPublished(ctx, tx, e.ID); uerr != nil {
				return uerr
			}
			published++
			w.metrics.recordOutcome(outcomeSuccess)
		}
		return nil
	})
	return published, err
}

// selectBatch returns up to limit unpublished events whose retry
// window has arrived (next_retry_at <= NOW()). The query uses
// FOR UPDATE SKIP LOCKED so concurrent Workers (or a manually
// running worker + a CRON replay tool) don't collide on the same
// rows. maxAttempts > 0 filters out events that have already
// failed that many times — they remain in the table for manual
// disposition (DLQ workflow).
//
// Ordering by (next_retry_at, created_at) keeps fresh inserts at
// the front of the queue while still draining backed-off failures
// in FIFO once their window opens.
func selectBatch(ctx context.Context, q db.Querier, limit, maxAttempts int) ([]Event, error) {
	const baseSQL = `
		SELECT id, aggregate_type, aggregate_id, event_type, payload,
		       headers, created_at, attempts
		FROM outbox
		WHERE published_at IS NULL AND next_retry_at <= NOW()
	`
	sql := baseSQL
	args := []any{limit}
	if maxAttempts > 0 {
		sql += " AND attempts < $2"
		args = []any{limit, maxAttempts}
	}
	sql += `
		ORDER BY next_retry_at, created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var (
			e           Event
			headersJSON []byte
		)
		if err := rows.Scan(
			&e.ID, &e.AggregateType, &e.AggregateID, &e.EventType, &e.Payload,
			&headersJSON, &e.CreatedAt, &e.Attempts,
		); err != nil {
			return nil, err
		}
		if len(headersJSON) > 0 {
			if jerr := json.Unmarshal(headersJSON, &e.Headers); jerr != nil {
				// A malformed headers cell shouldn't poison the whole
				// batch — drop the headers, surface the row anyway, let
				// the operator notice via the published-without-headers
				// signal.
				e.Headers = nil
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// markPublished sets published_at = NOW() for the supplied event.
// Called from inside the fetch transaction, so the row's lock is
// already held and the UPDATE is a single tuple touch.
func markPublished(ctx context.Context, q db.Querier, id uuid.UUID) error {
	const sql = `UPDATE outbox SET published_at = NOW() WHERE id = $1`
	_, err := q.Exec(ctx, sql, id)
	return err
}

// markFailed bumps attempts + records last_error + slides
// next_retry_at forward by the supplied backoff delay. The row
// stays unpublished so the next tick AFTER the backoff window
// picks it up (subject to maxAttempts filter on the SELECT).
//
// retryAfter is bound as a Postgres INTERVAL literal string
// (e.g. "1000 microseconds") because pgx binds Go's int64 as
// PG bigint, and `bigint || ' microseconds'` has no `||` operator
// — the cast must happen at the SQL boundary, not via implicit
// coercion.
func markFailed(ctx context.Context, q db.Querier, id uuid.UUID, msg string, retryAfter time.Duration) error {
	const sql = `
		UPDATE outbox
		SET attempts = attempts + 1,
		    last_error = $2,
		    next_retry_at = NOW() + $3::interval
		WHERE id = $1
	`
	interval := fmt.Sprintf("%d microseconds", retryAfter.Microseconds())
	_, err := q.Exec(ctx, sql, id, msg, interval)
	return err
}

// backoffFor returns the wait duration before the Nth (1-indexed)
// retry: base * 2^(attempt-1), capped at max. base ≤ 0 disables
// backoff entirely (returns 0 — eligible immediately).
func backoffFor(attempt int, base, max time.Duration) time.Duration {
	if base <= 0 || attempt <= 0 {
		return 0
	}
	// Use int64 arithmetic to avoid overflow at high attempt counts;
	// the loop bails as soon as we hit max.
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d <= 0 || d >= max {
			return max
		}
	}
	return d
}
