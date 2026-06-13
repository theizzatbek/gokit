package cronmap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"

	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/sentrykit"
)

// Outcome labels used in metrics + log fields.
const (
	outcomeSuccess = "success"
	outcomeFailure = "failure"
	outcomeTimeout = "timeout"
)

// Runtime is the running scheduler returned by [Engine.Build]. Start
// kicks the *cron.Cron goroutine; Stop drains in-flight runs and
// shuts cron down.
type Runtime struct {
	jobs       []plannedJob
	locker     SingletonLocker
	logger     *slog.Logger
	useSentry  bool
	collectors *metricsCollector
	cronCfg    cron.Option

	onTickStart    func(ctx context.Context, name string)
	onTickComplete func(ctx context.Context, name string, err error, elapsed time.Duration)

	// states is keyed by job name. Counters / lastRun / paused are
	// mutated from concurrent ticks; reads via Stats / NextRun take
	// the state's mu briefly. Map itself is built once at Build and
	// never mutated.
	states map[string]*jobState

	mu      sync.Mutex
	c       *cron.Cron
	wg      sync.WaitGroup
	started bool
	stopped bool
	runCtx  context.Context    // captured at Start; carries shutdown signal
	cancel  context.CancelFunc // invoked at Stop to propagate to in-flight handlers
}

// jobState carries the mutable per-job counters + last/next-run
// timestamps shown in [JobStats]. atomic counters keep the hot path
// lock-free; lastRunMu protects the time + outcome + duration trio
// because they must move together (writing one without the others
// would surface an inconsistent /admin snapshot).
type jobState struct {
	totalRuns    atomic.Int64
	successCount atomic.Int64
	failureCount atomic.Int64
	timeoutCount atomic.Int64
	skipped      atomic.Int64 // singleton or paused skips
	paused       atomic.Bool

	lastRunMu       sync.Mutex
	lastRunAt       time.Time
	lastOutcome     string
	lastRunDuration time.Duration
}

func (s *jobState) recordRun(at time.Time, outcome string, dur time.Duration) {
	if s == nil {
		return
	}
	s.totalRuns.Add(1)
	switch outcome {
	case outcomeSuccess:
		s.successCount.Add(1)
	case outcomeFailure:
		s.failureCount.Add(1)
	case outcomeTimeout:
		s.timeoutCount.Add(1)
	}
	s.lastRunMu.Lock()
	s.lastRunAt = at
	s.lastOutcome = outcome
	s.lastRunDuration = dur
	s.lastRunMu.Unlock()
}

// JobStats is the per-job snapshot returned in [Runtime.Stats].
type JobStats struct {
	Name            string
	Paused          bool
	TotalRuns       int64
	SuccessCount    int64
	FailureCount    int64
	TimeoutCount    int64
	SkippedCount    int64
	LastRunAt       time.Time
	LastOutcome     string
	LastRunDuration time.Duration
	NextRunAt       time.Time
}

// Stats returns the cheap per-job snapshot — suitable for /admin or
// /healthz endpoints. NextRunAt is computed against time.Now via the
// job's parsed cron.Schedule.
//
// Nil receiver returns nil.
func (r *Runtime) Stats() []JobStats {
	if r == nil {
		return nil
	}
	now := time.Now()
	out := make([]JobStats, len(r.jobs))
	for i := range r.jobs {
		j := &r.jobs[i]
		s := r.states[j.name]
		js := JobStats{
			Name:         j.name,
			TotalRuns:    s.totalRuns.Load(),
			SuccessCount: s.successCount.Load(),
			FailureCount: s.failureCount.Load(),
			TimeoutCount: s.timeoutCount.Load(),
			SkippedCount: s.skipped.Load(),
			Paused:       s.paused.Load(),
			NextRunAt:    j.schedule.Next(now),
		}
		s.lastRunMu.Lock()
		js.LastRunAt = s.lastRunAt
		js.LastOutcome = s.lastOutcome
		js.LastRunDuration = s.lastRunDuration
		s.lastRunMu.Unlock()
		out[i] = js
	}
	return out
}

// NextRun returns the next scheduled fire time for the named job.
// Returns *errs.Error{KindNotFound} when the name is unknown. nil
// receiver returns the zero time and a NotFound error.
func (r *Runtime) NextRun(name string) (time.Time, error) {
	if r == nil {
		return time.Time{}, xerrs.NotFoundf(CodeUnknownJob,
			"cronmap: unknown job %q", name)
	}
	for i := range r.jobs {
		if r.jobs[i].name == name {
			return r.jobs[i].schedule.Next(time.Now()), nil
		}
	}
	return time.Time{}, xerrs.NotFoundf(CodeUnknownJob,
		"cronmap: unknown job %q", name)
}

// PauseJob pauses the named job — subsequent ticks skip it (counted
// in cronmap_singleton_skipped_total under a "paused" reason in
// future versions; for v1 they accrue under JobStats.SkippedCount
// and emit a Debug log). Returns NotFound when the name is unknown.
//
// Idempotent: pausing an already-paused job is a no-op.
func (r *Runtime) PauseJob(name string) error {
	if r == nil {
		return xerrs.NotFoundf(CodeUnknownJob, "cronmap: unknown job %q", name)
	}
	s, ok := r.states[name]
	if !ok {
		return xerrs.NotFoundf(CodeUnknownJob, "cronmap: unknown job %q", name)
	}
	s.paused.Store(true)
	if r.logger != nil {
		r.logger.Info("cronmap: job paused", "name", name)
	}
	return nil
}

// ResumeJob unpauses the named job. NotFound when unknown.
// Idempotent.
func (r *Runtime) ResumeJob(name string) error {
	if r == nil {
		return xerrs.NotFoundf(CodeUnknownJob, "cronmap: unknown job %q", name)
	}
	s, ok := r.states[name]
	if !ok {
		return xerrs.NotFoundf(CodeUnknownJob, "cronmap: unknown job %q", name)
	}
	s.paused.Store(false)
	if r.logger != nil {
		r.logger.Info("cronmap: job resumed", "name", name)
	}
	return nil
}

// OverrideOK is the explicit opt-in token required by
// [Runtime.TriggerJob]. Pass `cronmap.OverrideOK{}` at every call
// site. The empty-struct shape is zero-cost at runtime; the value
// exists purely as a signature-level marker that the caller knows
// they are bypassing the singleton (leader-election) lock and the
// pause flag. Greppable on `OverrideOK` so an /admin endpoint that
// forwards this call surfaces in audit without folklore knowledge
// of what "TriggerJob" really means.
type OverrideOK struct{}

// TriggerJob fires the named job out-of-band synchronously on the
// caller's goroutine. Skips the singleton lock + paused check by
// design — operator-driven /admin actions should run regardless. The
// retry loop, timeout, hooks, metrics, and Stats counters all fire
// as on a normal tick.
//
// The third argument is an explicit [OverrideOK] token — see that
// type's doc for the rationale. The token has no runtime state; it
// is a code-review gate so a "I just want to fire this job manually"
// path cannot land in production code without surfacing in greps.
//
// Returns NotFound when the name is unknown. Nil-receiver returns
// NotFound. The runtime need NOT be Started — TriggerJob works on a
// stopped runtime too (manual-only mode).
func (r *Runtime) TriggerJob(ctx context.Context, name string, _ OverrideOK) error {
	if r == nil {
		return xerrs.NotFoundf(CodeUnknownJob, "cronmap: unknown job %q", name)
	}
	var found *plannedJob
	for i := range r.jobs {
		if r.jobs[i].name == name {
			found = &r.jobs[i]
			break
		}
	}
	if found == nil {
		return xerrs.NotFoundf(CodeUnknownJob, "cronmap: unknown job %q", name)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.runOnce(ctx, *found, true /* skipSingleton */)
	return nil
}

// JobNames returns the registered job names, sorted. Nil-safe (zero
// slice on nil receiver). Convenient for /debug/info-style
// introspection.
func (r *Runtime) JobNames() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.jobs))
	for i, j := range r.jobs {
		out[i] = j.name
	}
	sort.Strings(out)
	return out
}

// Start kicks the cron tick goroutines. The passed ctx is the parent
// of the runtime's internal cancellation chain — when ctx is
// cancelled (or [Runtime.Stop] is called), in-flight handler
// invocations receive a cancelled ctx.
//
// Idempotent: a second Start call returns nil (logged as a no-op).
// After Stop the runtime is single-use; Start returns
// *errs.Error{Code: [CodeRuntimeStopped]}.
//
// Nil receiver: no-op (returns nil).
func (r *Runtime) Start(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return xerrs.Validation(CodeRuntimeStopped,
			"cronmap: Start called after Stop (runtimes are single-use; build a new engine)")
	}
	if r.started {
		if r.logger != nil {
			r.logger.Info("cronmap: Start called twice — no-op")
		}
		return nil
	}
	r.started = true
	r.runCtx, r.cancel = context.WithCancel(ctx)

	r.c = cron.New(r.cronCfg)
	for i := range r.jobs {
		j := r.jobs[i]
		r.c.Schedule(j.schedule, cron.FuncJob(func() { r.tick(j) }))
	}
	r.c.Start()
	if r.logger != nil {
		r.logger.Info("cronmap: scheduler started", "jobs", len(r.jobs))
	}
	return nil
}

// Stop signals shutdown and waits for in-flight runs to return. The
// drain deadline comes from the passed ctx — pass
// context.WithTimeout(...) for a bounded wait. If ctx has no
// deadline a 5s default applies so a hung handler doesn't pin the
// caller forever.
//
// Idempotent: a second Stop call returns nil immediately. Calling
// Stop before Start returns nil.
//
// Nil receiver: no-op (returns nil).
func (r *Runtime) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.stopped || !r.started {
		r.stopped = true
		r.mu.Unlock()
		return nil
	}
	r.stopped = true
	c := r.c
	cancel := r.cancel
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	cronStopCtx := c.Stop()

	deadline := 5 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d > 0 {
			deadline = d
		}
	}

	start := time.Now()
	select {
	case <-cronStopCtx.Done():
	case <-time.After(deadline):
	}
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	remaining := deadline - time.Since(start)
	if remaining < 0 {
		remaining = 0
	}
	select {
	case <-done:
	case <-time.After(remaining):
	}
	if r.logger != nil {
		r.logger.Info("cronmap: runtime stopped", "drain_ms", time.Since(start).Milliseconds())
	}
	return nil
}

// tick is the wrapped per-job invocation invoked by robfig/cron.
// Layered: cancellation guard → paused guard → singleton lock →
// retry loop → sentry monitor → per-run timeout → fn → metrics + log.
func (r *Runtime) tick(j plannedJob) {
	if r.runCtx == nil {
		return
	}
	if err := r.runCtx.Err(); err != nil {
		return
	}
	r.runOnce(r.runCtx, j, false /* skipSingleton */)
}

// runOnce executes one invocation cycle for j against ctx. When
// skipSingleton is true the leader lock is bypassed — used by
// [TriggerJob] for operator-driven runs. Pause checks always apply
// to the scheduler tick path; TriggerJob skips both.
func (r *Runtime) runOnce(ctx context.Context, j plannedJob, skipSingleton bool) {
	state := r.states[j.name]

	// Paused guard — only on scheduler ticks.
	if !skipSingleton && state.paused.Load() {
		state.skipped.Add(1)
		if r.logger != nil {
			r.logger.Debug("cronmap: tick skipped — paused", "name", j.name)
		}
		return
	}

	r.wg.Add(1)
	defer r.wg.Done()

	// Singleton lock.
	if j.singleton && !skipSingleton {
		release, ok, err := r.locker.TryLock(ctx, j.name)
		if err != nil {
			r.observeOutcome(state, j.name, outcomeFailure, 0, fmt.Errorf("singleton lock: %w", err))
			return
		}
		if !ok {
			state.skipped.Add(1)
			r.collectors.incSingletonSkip(j.name)
			if r.logger != nil {
				r.logger.Debug("cronmap: singleton skipped", "name", j.name)
			}
			return
		}
		defer release()
	}

	r.fireOnTickStart(ctx, j.name)

	totalStart := time.Now()
	var finalErr error
retryLoop:
	for attempt := 0; attempt <= j.maxRetries; attempt++ {
		if attempt > 0 {
			wait := j.retryBackoffFor(attempt)
			if wait > 0 {
				select {
				case <-ctx.Done():
					// Context cancelled during backoff: stop retrying
					// and surface the cancellation. A bare `break` here
					// would only leave the select and run another attempt.
					finalErr = ctx.Err()
					break retryLoop
				case <-time.After(wait):
				}
			}
		}
		runCtx := ctx
		var cancel context.CancelFunc
		if j.timeout > 0 {
			runCtx, cancel = context.WithTimeout(ctx, j.timeout)
		}

		var attemptErr error
		if r.useSentry {
			attemptErr = sentrykit.MonitorCron(runCtx, j.slug,
				func(c context.Context) error { return invokeRecovering(c, j.handler) })
		} else {
			attemptErr = invokeRecovering(runCtx, j.handler)
		}
		if cancel != nil {
			cancel()
		}
		if attemptErr == nil {
			finalErr = nil
			break
		}
		finalErr = attemptErr
		// DeadlineExceeded under per-run timeout is a "transient"
		// signal for retry purposes too — keep going until
		// MaxRetries exhausted, then classify as timeout outcome.
	}
	dur := time.Since(totalStart)

	outcome := outcomeSuccess
	if finalErr != nil {
		if errors.Is(finalErr, context.DeadlineExceeded) {
			outcome = outcomeTimeout
		} else {
			outcome = outcomeFailure
		}
	}
	r.observeOutcome(state, j.name, outcome, dur, finalErr)
	r.fireOnTickComplete(ctx, j.name, finalErr, dur)
}

// retryBackoffFor returns the wait duration before attempt N
// (1-indexed). Base × 2^(N-1) capped at base × 8.
func (j plannedJob) retryBackoffFor(attempt int) time.Duration {
	if j.retryBackoff <= 0 || attempt <= 1 {
		return j.retryBackoff
	}
	shift := attempt - 1
	if shift > 3 {
		shift = 3 // cap at base × 8
	}
	return j.retryBackoff << shift
}

// observeOutcome funnels metrics, log, and state-counter updates for
// a finished run. err may be nil on success.
func (r *Runtime) observeOutcome(state *jobState, name, outcome string, dur time.Duration, err error) {
	state.recordRun(time.Now(), outcome, dur)
	r.collectors.observeRun(name, outcome, dur)
	if err == nil {
		return
	}
	if r.logger == nil {
		return
	}
	switch outcome {
	case outcomeTimeout:
		r.logger.Warn("cronmap: job timed out",
			"name", name, "err", err.Error())
	default:
		r.logger.Warn("cronmap: job failed",
			"name", name, "err", err.Error())
	}
}

// fireOnTickStart invokes the configured hook with panic recovery.
func (r *Runtime) fireOnTickStart(ctx context.Context, name string) {
	if r.onTickStart == nil {
		return
	}
	defer func() {
		if p := recover(); p != nil && r.logger != nil {
			r.logger.Warn("cronmap: OnTickStart panic recovered",
				"name", name, "panic", fmt.Sprint(p))
		}
	}()
	r.onTickStart(ctx, name)
}

// fireOnTickComplete invokes the configured hook with panic recovery.
func (r *Runtime) fireOnTickComplete(ctx context.Context, name string, err error, elapsed time.Duration) {
	if r.onTickComplete == nil {
		return
	}
	defer func() {
		if p := recover(); p != nil && r.logger != nil {
			r.logger.Warn("cronmap: OnTickComplete panic recovered",
				"name", name, "panic", fmt.Sprint(p))
		}
	}()
	r.onTickComplete(ctx, name, err, elapsed)
}

// invokeRecovering runs fn under a recover so a handler panic is
// turned into a regular failure-outcome metric + log entry. The cron
// entry stays armed for the next tick — same convention as
// service.WithCron.
func invokeRecovering(ctx context.Context, fn HandlerFn) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("cronmap: handler panic: %v", p)
		}
	}()
	return fn(ctx)
}
