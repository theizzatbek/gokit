package service

import (
	"context"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/sentrykit"
)

// JobFn is the function signature for scheduled tasks. Receives a
// fresh ctx derived from the service's root ctx — honour ctx.Done
// for cooperative cancellation on Shutdown.
type JobFn func(ctx context.Context) error

// CronJob is one configured cron entry.
type CronJob struct {
	// Name is the human-readable label surfaced in logs + (when
	// Sentry is wired) used as the Crons monitor slug.
	Name string

	// Schedule is a standard 5-field cron expression
	// (minute hour day-of-month month day-of-week) per robfig/cron's
	// default parser. Add "0" as a leading field for second-level
	// precision via [WithCronParser].
	Schedule string

	// Fn is invoked on every tick that the schedule fires.
	Fn JobFn
}

type cronConfig struct {
	jobs        []CronJob
	parser      cron.Parser
	customSlugs map[string]string // job name → custom Sentry slug
}

// CodeCronInvalidSchedule — the parser rejected the cron expression
// at scheduler boot.
const CodeCronInvalidSchedule = "service_cron_invalid_schedule"

// WithCron registers a recurring job. service.New starts the
// scheduler after all subsystems are built; the scheduler runs the
// job on every tick that schedule fires, on a single goroutine per
// job (overlapping ticks SKIP — the in-progress run blocks the
// queued one). When [WithSentry] is wired, each invocation
// auto-wraps with [sentrykit.MonitorCronWithConfig] using a slug
// derived from name (slugify(name)).
//
//	service.WithCron("daily-rollup", "0 3 * * *", rollups.Run)
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

// WithCronSlug overrides the Sentry Crons slug for a job. Default
// is the job name with non-identifier characters replaced by `-`
// (e.g. "Daily Rollup" → "daily-rollup"). Use this when multiple
// services share one Sentry project and you need explicit
// disambiguation: "orders-daily-rollup", "payments-daily-rollup".
//
// No effect unless [WithSentry] is also wired.
func WithCronSlug(jobName, slug string) Option {
	return func(o *options) {
		if o.cronSlugs == nil {
			o.cronSlugs = map[string]string{}
		}
		o.cronSlugs[jobName] = slug
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

// AddCron registers a job AFTER service.New has built the
// subsystems. Use when the job's closure needs `svc.DB` / `svc.Auth`
// / etc — config-time [WithCron] runs before those fields are
// populated, so post-build registration solves the chicken-and-egg
// problem.
//
//	svc, _ := service.New(...)
//	defer svc.Close()
//	svc.AddCron("daily-rollup", "0 3 * * *", func(ctx context.Context) error {
//	    return rollups.Run(ctx, svc.DB)
//	})
//
// Lazily constructs the scheduler when no [WithCron] jobs were
// registered at config time. Errors with [CodeCronInvalidSchedule]
// when the schedule string is rejected by the parser.
func (s *Service[T, C]) AddCron(name, schedule string, fn JobFn) error {
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
	useSentry := s.opts.sentryDSN != ""
	slug := s.cronSlug(name)
	sched := s.scheduler
	wrapped := func() {
		sched.wg.Add(1)
		defer sched.wg.Done()
		ctx := context.Background()
		run := func(rctx context.Context) error { return fn(rctx) }
		var err error
		if useSentry {
			err = sentrykit.MonitorCron(ctx, slug, run)
		} else {
			err = run(ctx)
		}
		if err != nil && s.logger != nil {
			s.logger.Warn("cron: job failed",
				"name", name, "schedule", schedule, "err", err.Error())
		}
	}
	if _, err := s.scheduler.c.AddFunc(schedule, wrapped); err != nil {
		return xerrs.Wrapf(err, xerrs.KindValidation, CodeCronInvalidSchedule,
			"service: cron schedule %q invalid for job %q", schedule, name)
	}
	return nil
}

// buildCron constructs the *cron.Cron from accumulated jobs and
// kicks it off. Returns an error if any schedule is invalid. Wires
// each job through the Sentry crons monitor when WithSentry was
// also passed.
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

	useSentry := s.opts.sentryDSN != ""
	for _, job := range s.opts.cronJobs {
		j := job // capture
		slug := s.cronSlug(j.Name)
		wrapped := func() {
			if err := ctx.Err(); err != nil {
				return
			}
			sched.wg.Add(1)
			defer sched.wg.Done()

			jobCtx := ctx
			run := func(rctx context.Context) error { return j.Fn(rctx) }
			var err error
			if useSentry {
				err = sentrykit.MonitorCron(jobCtx, slug, run)
			} else {
				err = run(jobCtx)
			}
			if err != nil && s.logger != nil {
				s.logger.Warn("cron: job failed",
					"name", j.Name, "schedule", j.Schedule, "err", err.Error())
			}
		}
		if _, err := c.AddFunc(j.Schedule, wrapped); err != nil {
			return xerrs.Wrapf(err, xerrs.KindValidation, CodeCronInvalidSchedule,
				"service: cron schedule %q invalid for job %q", j.Schedule, j.Name)
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

// cronSlug returns the Sentry Crons slug for a job — either the
// caller-supplied override or a slugified version of the job name.
func (s *Service[T, C]) cronSlug(jobName string) string {
	if s.opts.cronSlugs != nil {
		if v, ok := s.opts.cronSlugs[jobName]; ok && v != "" {
			return v
		}
	}
	return slugify(jobName)
}

// slugify converts an arbitrary label into a Sentry-friendly slug
// — lowercase, with non-[a-z0-9] runs collapsed to single dashes.
// Conservative output so the slug stays stable across Sentry's
// own validation rules.
func slugify(s string) string {
	out := make([]byte, 0, len(s))
	prevDash := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			out = append(out, c+32)
			prevDash = false
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			out = append(out, c)
			prevDash = false
		default:
			if !prevDash {
				out = append(out, '-')
				prevDash = true
			}
		}
	}
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return "job"
	}
	return string(out)
}
