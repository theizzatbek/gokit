package cronmap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
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

	mu      sync.Mutex
	c       *cron.Cron
	wg      sync.WaitGroup
	started bool
	stopped bool
	runCtx  context.Context    // captured at Start; carries shutdown signal
	cancel  context.CancelFunc // invoked at Stop to propagate to in-flight handlers
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
		// Use c.Schedule(s, FuncJob) with the pre-parsed Schedule
		// rather than c.AddFunc(stringExpr, fn) — schedules were
		// already validated at Build and we hold the Schedule object.
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
		// Either never started or already stopped — mark stopped so
		// future Start calls fail loud, but don't double-drain.
		r.stopped = true
		r.mu.Unlock()
		return nil
	}
	r.stopped = true
	c := r.c
	cancel := r.cancel
	r.mu.Unlock()

	// Cancel runCtx first so handlers observably-aware of ctx return
	// early; THEN stop cron's tick loop.
	if cancel != nil {
		cancel()
	}
	cronStopCtx := c.Stop() // returns the in-flight-jobs-done context

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
	// Belt + braces: WaitGroup catches any handler still pending after
	// cron's stop returns (it tracks the runtime wrap, not robfig/cron
	// internals).
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
// Layered: cancellation guard → singleton lock → sentry monitor →
// per-run timeout → fn → metrics + log.
func (r *Runtime) tick(j plannedJob) {
	// Cancellation guard: a tick can still fire between cancel() and
	// the cron stop completing.
	if r.runCtx == nil {
		return
	}
	if err := r.runCtx.Err(); err != nil {
		return
	}
	r.wg.Add(1)
	defer r.wg.Done()

	// Singleton lock — outermost so a failed acquire doesn't waste a
	// timeout / sentry round-trip.
	if j.singleton {
		release, ok, err := r.locker.TryLock(r.runCtx, j.name)
		if err != nil {
			r.observeFailure(j.name, fmt.Errorf("singleton lock: %w", err), 0)
			return
		}
		if !ok {
			r.collectors.incSingletonSkip(j.name)
			if r.logger != nil {
				r.logger.Debug("cronmap: singleton skipped", "name", j.name)
			}
			return
		}
		defer release()
	}

	parent := r.runCtx
	run := func(ctx context.Context) error {
		// Per-run timeout — innermost so the handler sees the
		// deadline ctx directly.
		if j.timeout > 0 {
			timed, cancel := context.WithTimeout(ctx, j.timeout)
			defer cancel()
			ctx = timed
		}
		return invokeRecovering(ctx, j.handler)
	}

	start := time.Now()
	var err error
	if r.useSentry {
		err = sentrykit.MonitorCron(parent, j.slug, run)
	} else {
		err = run(parent)
	}
	dur := time.Since(start)

	switch {
	case err == nil:
		r.collectors.observeRun(j.name, outcomeSuccess, dur)
	case errors.Is(err, context.DeadlineExceeded):
		r.collectors.observeRun(j.name, outcomeTimeout, dur)
		if r.logger != nil {
			r.logger.Warn("cronmap: job timed out",
				"name", j.name, "timeout", j.timeout, "err", err.Error())
		}
	default:
		r.observeFailure(j.name, err, dur)
	}
}

func (r *Runtime) observeFailure(name string, err error, dur time.Duration) {
	r.collectors.observeRun(name, outcomeFailure, dur)
	if r.logger != nil {
		r.logger.Warn("cronmap: job failed",
			"name", name, "err", err.Error())
	}
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
