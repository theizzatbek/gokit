package jobs

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/theizzatbek/gokit/db"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// HandlerFunc is the typed worker callback. Returning nil marks the
// job done; returning an error increments attempts and re-queues
// the row with exponential backoff (until max_attempts is hit, then
// the row moves to state `failed`).
//
// Handlers MUST honour ctx.Done() — Worker.Stop signals shutdown by
// cancelling the parent ctx; a stuck handler holds up the whole
// drain.
type HandlerFunc[T any] func(ctx context.Context, payload T) error

// handlerEntry is the type-erased registry record. The dispatch
// closure does the JSON-decode for the right T at runtime so the
// Worker doesn't have to be generic.
type handlerEntry struct {
	dispatch func(ctx context.Context, raw []byte) error
}

// Worker is the polling executor. Construct with [NewWorker], add
// handlers via [RegisterHandler], call [Worker.Start] to begin
// processing.
//
// Single-use: Start may only be called once per Worker; the second
// call returns CodeWorkerStarted. Multiple Worker instances may run
// concurrently (multi-pod) — each polls the same table and SKIP
// LOCKED keeps row-ownership exclusive.
type Worker struct {
	db       *db.DB
	logger   *slog.Logger
	metric   *workerMetrics
	interval time.Duration
	batch    int
	workerID string
	queues   []string

	mu       sync.Mutex
	handlers map[string]handlerEntry

	started atomic.Bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// WorkerOption tunes [NewWorker].
type WorkerOption func(*Worker)

// WithInterval sets the polling cadence. Default 1s — short enough
// for "near-real-time" delayed jobs, long enough not to saturate
// the table on idle services.
func WithInterval(d time.Duration) WorkerOption {
	return func(w *Worker) { w.interval = d }
}

// WithBatchSize caps the number of rows fetched per tick. Default
// 50 — balances throughput and lock-hold-time. Larger values
// amortise the round-trip but starve other workers if any one
// handler is slow.
func WithBatchSize(n int) WorkerOption {
	return func(w *Worker) { w.batch = n }
}

// WithWorkerID stamps locked_by on every claimed row. Default is
// the process PID; pass a stable name (k8s pod) so ops can join the
// stuck-row triage to a live process.
func WithWorkerID(id string) WorkerOption {
	return func(w *Worker) { w.workerID = id }
}

// WithQueues restricts the Worker to a subset of queue names.
// Default: drain every queue. Use for "billing-only" / "email-only"
// worker pools that scale independently.
func WithQueues(names ...string) WorkerOption {
	return func(w *Worker) { w.queues = names }
}

// WithLogger installs the *slog.Logger that records Warn on handler
// failure / Debug on successful dispatch. nil silences.
func WithLogger(l *slog.Logger) WorkerOption {
	return func(w *Worker) { w.logger = l }
}

// WithMetrics registers Prometheus collectors:
//
//   - jobs_processed_total{type, outcome}
//   - jobs_dispatch_duration_seconds{type}
//   - jobs_inflight (gauge)
//
// nil reg no-ops.
func WithMetrics(reg prometheus.Registerer) WorkerOption {
	return func(w *Worker) {
		if reg == nil {
			return
		}
		w.metric = newWorkerMetrics(reg)
	}
}

// NewWorker constructs a Worker bound to d. Returns *errs.Error
// when d is nil.
func NewWorker(d *db.DB, opts ...WorkerOption) (*Worker, error) {
	if d == nil {
		return nil, xerrs.Validation(CodeNilDB, "jobs: nil DB")
	}
	w := &Worker{
		db:       d,
		interval: time.Second,
		batch:    50,
		handlers: map[string]handlerEntry{},
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	for _, opt := range opts {
		opt(w)
	}
	return w, nil
}

// RegisterHandler binds a typed callback for jobType. T is the
// concrete payload struct the handler expects; the worker
// JSON-decodes each row's payload into T before dispatch.
//
// Panics on duplicate registration / empty jobType — these are
// programmer errors at startup.
func RegisterHandler[T any](w *Worker, jobType string, fn HandlerFunc[T]) {
	if w == nil {
		panic("jobs.RegisterHandler: nil Worker")
	}
	if jobType == "" {
		panic("jobs.RegisterHandler: empty jobType")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, exists := w.handlers[jobType]; exists {
		panic("jobs.RegisterHandler: duplicate jobType " + jobType)
	}
	w.handlers[jobType] = handlerEntry{
		dispatch: func(ctx context.Context, raw []byte) error {
			var payload T
			if err := json.Unmarshal(raw, &payload); err != nil {
				return xerrs.Wrap(err, xerrs.KindValidation, CodePayloadDecode,
					"jobs: payload decode failed")
			}
			return fn(ctx, payload)
		},
	}
}

// Start begins the polling loop. Blocks until ctx is cancelled OR
// Stop is called. Returns ctx.Err() on cancellation.
//
// Only one Start per Worker — the second call returns
// CodeWorkerStarted.
func (w *Worker) Start(ctx context.Context) error {
	if !w.started.CompareAndSwap(false, true) {
		return xerrs.Conflict(CodeWorkerStarted, "jobs: Start called twice")
	}
	if w.workerID == "" {
		w.workerID = defaultWorkerID()
	}
	defer close(w.doneCh)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Immediate first poll so a fresh deploy doesn't wait for the
	// first tick before draining backlog.
	w.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.stopCh:
			return nil
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

// Stop signals the polling loop to exit. Blocks until the loop has
// finished (so callers can defer Stop next to defer Close on the
// DB pool and be sure no handler is mid-flight).
//
// Safe to call multiple times — only the first call signals.
func (w *Worker) Stop() error {
	if !w.started.Load() {
		return nil
	}
	select {
	case <-w.stopCh: // already closed
	default:
		close(w.stopCh)
	}
	<-w.doneCh
	return nil
}

const claimSQL = `
UPDATE jobs SET
    state = 'running',
    locked_by = $1,
    locked_at = NOW(),
    attempts = attempts + 1
WHERE id IN (
    SELECT id FROM jobs
    WHERE state = 'queued'
      AND run_at <= NOW()
      AND ($3::text[] IS NULL OR queue = ANY($3::text[]))
    ORDER BY run_at
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
RETURNING id, type, payload, attempts, max_attempts
`

const doneSQL = `
UPDATE jobs SET state = 'done', finished_at = NOW(), last_error = NULL
WHERE id = $1
`

const retrySQL = `
UPDATE jobs SET
    state = 'queued',
    run_at = NOW() + ($2::interval),
    last_error = $3
WHERE id = $1
`

const failSQL = `
UPDATE jobs SET
    state = 'failed',
    finished_at = NOW(),
    last_error = $2
WHERE id = $1
`

// tick performs one polling cycle: claim up to batch rows, dispatch
// each, persist the outcome. Errors at the SQL level are logged but
// do not stop the loop.
func (w *Worker) tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	rows, err := w.db.Query(ctx, claimSQL, w.workerID, w.batch, queueFilter(w.queues))
	if err != nil {
		w.metric.recordPollErr()
		if w.logger != nil {
			w.logger.Warn("jobs: claim failed", "err", err.Error())
		}
		return
	}
	type claimed struct {
		id          int64
		jobType     string
		payload     []byte
		attempts    int
		maxAttempts int
	}
	var batch []claimed
	for rows.Next() {
		var c claimed
		if err := rows.Scan(&c.id, &c.jobType, &c.payload, &c.attempts, &c.maxAttempts); err != nil {
			if w.logger != nil {
				w.logger.Warn("jobs: scan failed", "err", err.Error())
			}
			continue
		}
		batch = append(batch, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil && w.logger != nil {
		w.logger.Warn("jobs: rows err", "err", err.Error())
	}

	for _, c := range batch {
		w.metric.incInflight()
		w.dispatch(ctx, c.id, c.jobType, c.payload, c.attempts, c.maxAttempts)
		w.metric.decInflight()
	}
}

// dispatch runs one job's handler and persists the outcome.
func (w *Worker) dispatch(ctx context.Context, id int64, jobType string, payload []byte, attempts, maxAttempts int) {
	start := time.Now()

	w.mu.Lock()
	entry, ok := w.handlers[jobType]
	w.mu.Unlock()
	if !ok {
		w.persistFailure(ctx, id, "no handler registered for "+jobType)
		w.metric.observe(jobType, time.Since(start), "missing_handler")
		if w.logger != nil {
			w.logger.Warn("jobs: handler not registered",
				"job_id", id, "type", jobType)
		}
		return
	}

	err := entry.dispatch(ctx, payload)
	if err == nil {
		if _, derr := w.db.Exec(ctx, doneSQL, id); derr != nil && w.logger != nil {
			w.logger.Warn("jobs: mark done failed", "job_id", id, "err", derr.Error())
		}
		w.metric.observe(jobType, time.Since(start), "ok")
		return
	}

	if attempts >= maxAttempts {
		w.persistFailure(ctx, id, err.Error())
		w.metric.observe(jobType, time.Since(start), "failed")
		if w.logger != nil {
			w.logger.Warn("jobs: max attempts exhausted",
				"job_id", id, "type", jobType, "attempts", attempts, "err", err.Error())
		}
		return
	}

	backoff := computeBackoff(attempts)
	if _, derr := w.db.Exec(ctx, retrySQL, id, backoff.String(), err.Error()); derr != nil && w.logger != nil {
		w.logger.Warn("jobs: requeue failed", "job_id", id, "err", derr.Error())
	}
	w.metric.observe(jobType, time.Since(start), "retry")
	if w.logger != nil {
		w.logger.Debug("jobs: requeued",
			"job_id", id, "type", jobType, "attempts", attempts,
			"backoff", backoff, "err", err.Error())
	}
}

func (w *Worker) persistFailure(ctx context.Context, id int64, msg string) {
	if _, err := w.db.Exec(ctx, failSQL, id, msg); err != nil && w.logger != nil {
		w.logger.Warn("jobs: mark failed failed", "job_id", id, "err", err.Error())
	}
}

// computeBackoff returns the next-retry delay using exponential
// backoff capped at 1h with ±10% jitter. attempts == 1 yields ~1s;
// attempts == 10 yields ~17min.
func computeBackoff(attempts int) time.Duration {
	const base = float64(time.Second)
	const cap = float64(time.Hour)
	exp := base * math.Pow(2, float64(attempts-1))
	if exp > cap {
		exp = cap
	}
	jitter := 1 + (rand.Float64()*0.2 - 0.1) // ±10%
	return time.Duration(exp * jitter)
}

// queueFilter returns a TEXT[] PostgreSQL array when queues is
// non-empty, or nil to skip the queue-filter clause.
func queueFilter(queues []string) any {
	if len(queues) == 0 {
		return nil
	}
	return queues
}

func defaultWorkerID() string {
	return "jobs-worker"
}
