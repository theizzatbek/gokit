// Package cronmap is the kit's declarative cron scheduler — symmetric
// to fibermap (HTTP routes), clients/apimap (outbound calls), and
// clients/natsmap (NATS pub/sub).
//
// Jobs live in crons.yaml:
//
//	jobs:
//	  - name: daily-rollup
//	    handler: rollups.daily
//	    schedule: "0 3 * * *"
//	    timeout: 5m
//	    singleton: true
//	    sentry_slug: orders-daily-rollup
//
// Go code registers handlers by name and runs the engine:
//
//	eng := cronmap.New()
//	if err := eng.LoadFile("crons.yaml"); err != nil { return err }
//	cronmap.RegisterHandler(eng, "rollups.daily",
//	    func(ctx context.Context) error { return rollups.Daily(ctx, db) })
//	rt, err := eng.Build(
//	    cronmap.WithLogger(logger),
//	    cronmap.WithMetrics(reg),
//	    cronmap.WithSingletonLocker(cronmap.PGLocker(db)),
//	    cronmap.WithSentry(),
//	)
//	if err != nil { return err }
//	if err := rt.Start(ctx); err != nil { return err }
//	defer rt.Stop(ctx)
//
// Why not just [service.WithCron]?
//
// service.WithCron(name, schedule, fn) stays — it's the right
// primitive for jobs whose schedule is computed at startup or whose
// closure captures call-site state. cronmap is the declarative
// alternative: every job in one YAML file, ops change schedules by
// editing config rather than re-deploying.
//
// Cross-cutting features (per-run timeout, singleton leader-elect,
// Sentry crons slug) become YAML fields instead of new With* options,
// keeping the surface small.
//
// What cronmap does NOT do:
//
//   - It's not a payload-carrying job queue. For "send the welcome
//     email 5 minutes after signup", use db/jobs.
//   - It does NOT serialise concurrent runs of the same job within
//     one process. Set singleton: true and provide a Locker if you
//     need that guarantee across pods; in-process serialisation is
//     a v2 option.
//   - It does NOT recover panics SILENTLY. A panic in a handler is
//     turned into a failure-outcome metric + log entry, and the
//     entry stays armed for the next tick — same convention as
//     service.WithCron.
package cronmap
