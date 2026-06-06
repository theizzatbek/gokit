# cronmap

Декларативный cron-планировщик поверх YAML, симметричный `fibermap`/`apimap`/`natsmap`.

## `cronmap/`

Declarative cron scheduler at the module root, symmetric to `fibermap` (HTTP routes), `clients/apimap` (outbound calls), `clients/natsmap` (pub/sub). Jobs declared in `crons.yaml`: each entry has `name`, `handler`, `schedule` (cron expression) plus optional `timeout` (per-run deadline), `singleton: true` (leader election), `sentry_slug` (Sentry Crons monitor identity). Go code calls `RegisterHandler(eng, name, fn func(context.Context) error)` for each YAML entry; the runtime invocation chain layers singleton → sentry → per-run timeout → handler with panic recovery.

Lifecycle: `New → LoadFile/LoadBytes (n) → RegisterHandler (n) → Build (once) → Runtime → Start/Stop`. After Build the engine is sealed (further Load*/Register* panic, same MustCompile convention as fibermap). Build aggregates per-job validation through `errors.Join` so callers see every problem at once (`cronmap_missing_name`, `cronmap_missing_handler`, `cronmap_missing_schedule`, `cronmap_invalid_schedule`, `cronmap_invalid_timeout`, `cronmap_duplicate_job`, `cronmap_unknown_handler`, `cronmap_singleton_needs_locker`, `cronmap_already_built`, `cronmap_already_registered`, `cronmap_runtime_stopped`, `cronmap_env_var_unset`/`cronmap_env_var_malformed`).

Default parser is 5-field (no seconds); `WithParser(cron.Parser)` overrides. `${VAR}` substitution at LoadBytes time (`WithEnv(map[string]string)`) — same engine apimap/natsmap use.

`SingletonLocker` is a tiny interface (`TryLock(ctx, key) (release, ok, err)`) — `db/lock` matches it; kit ships `PGLocker(*db.DB)` as the default. `singleton: true` + no `WithSingletonLocker` at Build is the loud-failure path (caught at Build, not at first tick).

`WithSentry()` enables `sentrykit.MonitorCron(slug, fn)` wrapping; no Sentry import is pulled in unless the option is set.

Collectors when Metrics is set: `cronmap_runs_total{name,outcome}` (`success`/`failure`/`timeout` — `timeout` is its OWN label, not bundled into failure), `cronmap_run_duration_seconds{name}` Histogram, `cronmap_singleton_skipped_total{name}` Counter (separate from runs — N-1 of N pods skipping is the HEALTHY state, bundling it into outcome=failure would noise alert dashboards), `cronmap_jobs` Gauge (set once at Build).

Stop drains in-flight handlers under a deadline derived from the passed ctx (5s default when no deadline); `cancel(runCtx)` fires FIRST so ctx-aware handlers exit early before robfig/cron's tick loop stops. `(*Runtime)(nil)` is a safe no-op receiver. Handler panic → `failure` outcome metric + Warn log, NOT a process kill (same convention as `service.WithCron`).

service-side integration: `service.WithCronMap()` Option enables auto-build from `Config.CronMap.Path` (or `DefaultCronMapPath = "crons.yaml"` joined with ConfigsDir); `service.RegisterCronHandler(name, fn) Option` buffers handlers and applies them to the engine BEFORE Build (same shape as `WithAPIMapRegistration`); auto-wires PGLocker unconditionally when DB is configured (YAML-flip to singleton: true does NOT require redeploy) and `WithSentry()` when `s.sentryShutdown != nil`; runtime started under `s.runCtx`; Stop registered via OnShutdown chain. cronmap-level `cronmap_singleton_needs_locker` re-wraps to `service_cronmap_needs_db` so dashboards searching for `service_*` codes find it. `service.WithCron` stays for ad-hoc jobs whose schedule is computed at startup or whose closure captures call-site state — cronmap is the declarative alternative, NOT the replacement.

Depends on `errs/`, `robfig/cron/v3`, `gopkg.in/yaml.v3`; optionally `db/lock` for PGLocker and `sentrykit` for MonitorCron wrap.

**Retry policy:** YAML `max_retries` + `retry_backoff` (default 0 = no retry, back-compat). Retry on err / timeout / panic; backoff doubles per attempt capped at base × 8; successful retries surface as `success` outcome (not `failure`).

**Lifecycle hooks:** `WithOnTickStart(func(ctx, name string))` + `WithOnTickComplete(func(ctx, name, err, elapsed))` — panic-safe; multiple calls last wins.

**/admin endpoints:** `Runtime.Stats() []JobStats` returns per-job `{Name, Paused, TotalRuns, SuccessCount, FailureCount, TimeoutCount, SkippedCount, LastRunAt, LastOutcome, LastRunDuration, NextRunAt}` — atomic counters + mu only for last-run trio; nil-receiver safe. `Runtime.NextRun(name) (time.Time, error)` predicts via `Schedule.Next(time.Now())`. `Runtime.TriggerJob(ctx, name) error` fires the job synchronously bypassing singleton lock + paused guard (operator override convention); works on stopped runtime too. `Runtime.PauseJob(name) / ResumeJob(name) error` toggle scheduler-tick guard per job; paused jobs accumulate `JobStats.SkippedCount`; TriggerJob ignores pause by design. Stable code: `cronmap_unknown_job`.