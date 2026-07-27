package svckit

import (
	"context"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// JobFn is the function signature for scheduled tasks. Receives a
// fresh ctx derived from the service's root ctx — honour ctx.Done
// for cooperative cancellation on Shutdown.
type JobFn func(ctx context.Context) error

// CronJob is one configured cron entry.
type CronJob struct {
	// Name is the human-readable label surfaced in logs.
	Name string

	// Schedule is a standard 5-field cron expression
	// (minute hour day-of-month month day-of-week) per robfig/cron's
	// default parser. Add "0" as a leading field for second-level
	// precision via [WithCronParser].
	Schedule string

	// Fn is invoked on every tick that the schedule fires.
	Fn JobFn

	// Singleton enables pg_try_advisory_lock-based leader election
	// so only one replica runs the job per tick. See
	// [WithSingletonCron] for the full contract.
	Singleton bool
}

// CodeCronInvalidSchedule — the parser rejected the cron expression
// at scheduler boot.
const CodeCronInvalidSchedule = "svckit_cron_invalid_schedule"

// WithCron registers a recurring job. New starts the scheduler after
// all subsystems are built; the scheduler runs the job on every tick
// that schedule fires, on a single goroutine per job (overlapping
// ticks SKIP — the in-progress run blocks the queued one).
//
//	svckit.WithCron("daily-rollup", "0 3 * * *", rollups.Run)
//
// schedule uses the standard 5-field cron format by default; for
// second-level precision, configure the parser via
// [WithCronParser].
func WithCron(name, schedule string, fn JobFn) Option {
	return func(o *options) {
		o.cronJobs = append(o.cronJobs, CronJob{
			Name: name, Schedule: schedule, Fn: fn,
		})
	}
}

// WithCronParser overrides the cron expression parser. Default is
// the 5-field standard format (`m h dom mon dow`). Pass
// `cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)`
// to accept second-level precision ("@every 30s" / "0 * * * * *").
func WithCronParser(parser cron.Parser) Option {
	return func(o *options) { o.cronParser = parser }
}

// scheduler bundles the cron runtime + bookkeeping for graceful
// shutdown.
type scheduler struct {
	c    *cron.Cron
	stop chan struct{}
	wg   sync.WaitGroup
}

// wrapCronJob applies every decorator a mod registered via
// [Host.WrapCronJob] around fn, in registration order (each wrap
// layers over the previous result, so the last-registered decorator
// ends up outermost). Used for every tick the core schedules —
// [WithCron]/[WithSingletonCron] entries, [Service.AddCron]/
// [Service.AddSingletonCron] registrations, and the refresh-GC ticker
// — so a mod like sentrymod can restore per-tick monitoring without
// the core knowing what Sentry is.
func (s *Service[T, C]) wrapCronJob(fn JobFn) JobFn {
	if s.opts == nil {
		return fn
	}
	wrapped := fn
	for _, w := range s.opts.cronWrappers {
		wrapped = w(wrapped)
	}
	return wrapped
}

// jobCount returns the number of registered entries. Used by
// [Service.Status]. nil-safe.
func (s *scheduler) jobCount() int {
	if s == nil || s.c == nil {
		return 0
	}
	return len(s.c.Entries())
}

// AddCron registers a job AFTER New has built the subsystems. Use
// when the job's closure needs `svc.DB` / `svc.Auth` / etc —
// config-time [WithCron] runs before those fields are populated, so
// post-build registration solves the chicken-and-egg problem.
//
//	svc, _ := svckit.New(...)
//	defer svc.Close()
//	svc.AddCron("daily-rollup", "0 3 * * *", func(ctx context.Context) error {
//	    return rollups.Run(ctx, svc.DB)
//	})
//
// Lazily constructs the scheduler when no [WithCron] jobs were
// registered at config time. Errors with [CodeCronInvalidSchedule]
// when the schedule string is rejected by the parser.
func (s *Service[T, C]) AddCron(name, schedule string, fn JobFn) error {
	fn = s.wrapCronJob(fn)
	if s.scheduler == nil {
		parser := s.opts.cronParser
		emptyParser := cron.Parser{}
		if parser == emptyParser {
			parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
		}
		c := cron.New(cron.WithParser(parser))
		s.scheduler = &scheduler{c: c, stop: make(chan struct{})}
		c.Start()
		s.OnShutdown(func() error {
			stopCtx := c.Stop()
			select {
			case <-stopCtx.Done():
			case <-time.After(5 * time.Second):
			}
			s.scheduler.wg.Wait()
			return nil
		})
	}
	sched := s.scheduler
	wrapped := func() {
		// Skip the run when the service is already shutting down — the
		// runCancel chain has been invoked but the scheduler hasn't
		// fully stopped yet, so a tick can still fire.
		if s.runCtx != nil {
			if err := s.runCtx.Err(); err != nil {
				return
			}
		}
		sched.wg.Add(1)
		defer sched.wg.Done()
		// Hand the long-running, service-scoped ctx to the job so
		// observably-aware fn implementations can return at shutdown
		// via ctx.Done(). Falls back to Background only for tests that
		// construct *Service literals without going through [New].
		ctx := s.runCtx
		if ctx == nil {
			ctx = context.Background()
		}
		err := fn(ctx)
		if err != nil && s.logger != nil {
			s.logger.Warn("cron: job failed",
				"name", name, "schedule", schedule, "err", err.Error())
		}
	}
	if _, err := s.scheduler.c.AddFunc(schedule, wrapped); err != nil {
		return xerrs.Wrapf(err, xerrs.KindValidation, CodeCronInvalidSchedule,
			"svckit: cron schedule %q invalid for job %q", schedule, name)
	}
	return nil
}

// buildCron constructs the *cron.Cron from accumulated jobs and
// kicks it off. Returns an error if any schedule is invalid.
func (s *Service[T, C]) buildCron(ctx context.Context) error {
	if len(s.opts.cronJobs) == 0 {
		return nil
	}
	parser := s.opts.cronParser
	// Default to the standard 5-field cron parser (no seconds).
	emptyParser := cron.Parser{}
	if parser == emptyParser {
		parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	}
	c := cron.New(cron.WithParser(parser))

	sched := &scheduler{c: c, stop: make(chan struct{})}
	s.scheduler = sched

	for _, job := range s.opts.cronJobs {
		j := job // capture
		if j.Singleton && s.DB == nil {
			return xerrs.Validationf(CodeSingletonCronNeedsDB,
				"svckit: singleton cron %q requires DB to be configured", j.Name)
		}
		jobFn := j.Fn
		if j.Singleton {
			jobFn = s.wrapSingleton(j.Name, j.Fn)
		}
		jobFn = s.wrapCronJob(jobFn)
		wrapped := func() {
			// Use the service-scoped runCtx so shutdown propagates
			// into observably-aware fn implementations. The boot ctx
			// from [New] is intentionally NOT held: it has scope only
			// for boot and the caller may cancel it post-construct
			// without intending to terminate background jobs.
			jobCtx := s.runCtx
			if jobCtx == nil {
				jobCtx = context.Background()
			}
			if err := jobCtx.Err(); err != nil {
				return
			}
			sched.wg.Add(1)
			defer sched.wg.Done()

			err := jobFn(jobCtx)
			if err != nil && s.logger != nil {
				s.logger.Warn("cron: job failed",
					"name", j.Name, "schedule", j.Schedule, "err", err.Error())
			}
		}
		if _, err := c.AddFunc(j.Schedule, wrapped); err != nil {
			return xerrs.Wrapf(err, xerrs.KindValidation, CodeCronInvalidSchedule,
				"svckit: cron schedule %q invalid for job %q", j.Schedule, j.Name)
		}
	}
	c.Start()
	if s.logger != nil {
		s.logger.Info("cron: scheduler started", "jobs", len(s.opts.cronJobs))
	}
	s.OnShutdown(func() error {
		stopCtx := c.Stop()
		select {
		case <-stopCtx.Done():
		case <-time.After(5 * time.Second):
		}
		sched.wg.Wait()
		return nil
	})
	return nil
}
