package service

import (
	"context"
	"errors"

	"github.com/theizzatbek/gokit/db/lock"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Stable error Code constants for singleton-cron failures.
const (
	// CodeSingletonCronNeedsDB — WithSingletonCron requires
	// Config.DB to be configured (advisory locks live in Postgres).
	CodeSingletonCronNeedsDB = "service_singleton_cron_needs_db"

	// CodeSingletonCronLockAcquire — pg_try_advisory_lock returned an
	// unexpected error (not "false" — that's the expected skip path).
	CodeSingletonCronLockAcquire = "service_singleton_cron_lock_acquire"
)

// WithSingletonCron is [WithCron] with pg_try_advisory_lock-based
// leader election. In a multi-replica deployment, only ONE replica
// runs the job per tick — the rest see the lock held and skip
// silently (logged at Debug).
//
//	service.WithSingletonCron("daily-rollup", "0 3 * * *", rollup.Run)
//
// Lock semantics:
//
//   - Session-level: a dedicated pool conn holds the lock for the
//     duration of fn. Released cleanly on fn return (including panic).
//   - Key: first 8 bytes of `sha256(name)`. Stable across replicas as
//     long as they share the job name string.
//   - Skip on miss: pg_try_advisory_lock returns false → tick is
//     dropped silently. The next tick re-tries. No queueing.
//
// Failure modes:
//
//   - DB unavailable → lock acquisition errors, logged at Warn, fn
//     skipped. Recovers on the next tick.
//   - fn panics → defer releases the lock so the next tick can
//     acquire it again. The panic still propagates up to the
//     cron-runner's recover (built into robfig/cron) so the
//     scheduler stays alive.
//
// Requires Config.DB to be set. Errors at service.New time
// otherwise with [CodeSingletonCronNeedsDB].
func WithSingletonCron(name, schedule string, fn JobFn) Option {
	return func(o *options) {
		o.cronJobs = append(o.cronJobs, CronJob{
			Name: name, Schedule: schedule, Fn: fn, Singleton: true,
		})
	}
}

// AddSingletonCron is the post-build counterpart to
// [WithSingletonCron] — registers a singleton job AFTER
// service.New has finished building, so the job's closure can
// capture svc.DB / svc.Logger / etc.
func (s *Service[T, C]) AddSingletonCron(name, schedule string, fn JobFn) error {
	if s.DB == nil {
		return xerrs.Validation(CodeSingletonCronNeedsDB,
			"service: AddSingletonCron requires DB to be configured")
	}
	wrapped := s.wrapSingleton(name, fn)
	return s.AddCron(name, schedule, wrapped)
}

// wrapSingleton produces a JobFn that runs fn under a kit
// db/lock advisory lock. Reused by both buildCron (config-time
// jobs) and AddSingletonCron (post-build).
//
// Skip semantics: when another replica holds the lock,
// db/lock.TryAcquire returns (false, nil, nil), which we surface
// as a Debug log + nil return — the missed tick is the expected
// outcome, not an error.
func (s *Service[T, C]) wrapSingleton(name string, fn JobFn) JobFn {
	lk := lock.New(s.DB, "cron."+name)
	return func(ctx context.Context) error {
		acquired, release, err := lk.TryAcquire(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return xerrs.Wrap(err, xerrs.KindUnavailable, CodeSingletonCronLockAcquire,
				"service: singleton cron lock")
		}
		if !acquired {
			if s.logger != nil {
				s.logger.Debug("cron: singleton tick skipped (lock held elsewhere)",
					"name", name)
			}
			return nil
		}
		defer release()
		return fn(ctx)
	}
}
