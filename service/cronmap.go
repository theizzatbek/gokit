package service

import (
	"context"
	"errors"
	"os"

	"github.com/theizzatbek/gokit/cronmap"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Stable error Codes used by the cronmap-integration layer.
const (
	// CodeCronMapNeedsDB — Config.CronMap is enabled and the loaded
	// YAML has at least one job marked `singleton: true`, but DB is
	// not configured. The kit-default singleton backend is
	// PGLocker; without DB, leader election cannot be performed.
	CodeCronMapNeedsDB = "service_cronmap_needs_db"

	// CodeCronMapYAMLNotFound — explicit Config.CronMap.Path was
	// supplied but the file does not exist. The default-path case
	// is silently skipped (lets a service add cron jobs later
	// without forcing the file to exist on day one).
	CodeCronMapYAMLNotFound = "service_cronmap_yaml_not_found"
)

// WithCronMap enables cronmap auto-build using [DefaultCronMapPath]
// when no Config.CronMap.Path override is set. Equivalent to setting
// Config.CronMap.Enabled = true.
//
// The auto-build is opt-in for the same reason apimap / natsmap are:
// services that don't run periodic jobs should not silently load an
// empty crons.yaml.
//
// When the resolved path file does not exist:
//
//   - default path (no explicit Path) → silently skipped, svc.CronMap stays nil
//   - explicit Path → service.New fails with [CodeCronMapYAMLNotFound]
//
// Handlers are registered with [RegisterCronHandler] BEFORE
// service.New returns:
//
//	svc, err := service.New[Ctx, Claims](ctx, cfg, service.WithCronMap())
//	... // handlers must be registered before svc.Run / svc.AddCron etc.
func WithCronMap() Option {
	return func(o *options) { o.cronMapEnable = true }
}

// WithCronMapEnv supplies explicit values for ${VAR} substitution in
// cronmap's crons.yaml. Map is consulted before os.LookupEnv. Same
// shape as [WithAPIMapEnv] / [WithNATSMapEnv].
func WithCronMapEnv(m map[string]string) Option {
	return func(o *options) { o.cronMapEnv = m }
}

// RegisterCronHandler buffers a cronmap handler registration on the
// service options. The handler is applied to the underlying
// cronmap.Engine inside service.New BEFORE Build is called, so the
// YAML validation can match `handler:` references to live functions.
//
// Call order: AFTER [New] returns, RegisterCronHandler panics (the
// engine is sealed). The buffer pattern lets callers register
// handlers WITH options (functional config style) before subsystems
// are constructed, matching apimap's [WithAPIMapRegistration]
// convention.
//
//	svc, _ := service.New[Ctx, Claims](ctx, cfg, service.WithCronMap())
//	// FYI: this BEFORE-construct buffer is wrong — handlers must
//	// register through opts; see correct shape below.
//
// Typical use — register through options:
//
//	svc, _ := service.New[Ctx, Claims](ctx, cfg,
//	    service.WithCronMap(),
//	    service.RegisterCronHandler("rollups.daily", rollups.Daily),
//	    service.RegisterCronHandler("cleanup.hourly", cleanup.Hourly),
//	)
//
// RegisterCronHandler returns an [Option] rather than mutating a
// post-construct engine so handler registration is composable with
// other With* options at the call-site.
func RegisterCronHandler(name string, fn cronmap.HandlerFn) Option {
	return func(o *options) {
		if o.cronMapHandlers == nil {
			o.cronMapHandlers = map[string]cronmap.HandlerFn{}
		}
		o.cronMapHandlers[name] = fn
	}
}

// buildCronMap constructs the cronmap.Runtime from the loaded YAML +
// registered handlers, auto-wiring PGLocker when any job is
// singleton (and DB is present), and auto-enabling Sentry crons
// monitoring when sentryShutdown is present.
//
// Called from service.New after every other subsystem is built so
// DB / Sentry detection works. Failures surface as service-prefixed
// errors so callers can distinguish "wrong CRONMAP_PATH" from
// "yaml parse error".
func (s *Service[T, C]) buildCronMap(_ context.Context) error {
	path := resolvePathInDir(s.cfg.Service.ConfigsDir,
		s.cfg.CronMap.Path, DefaultCronMapPath,
		s.opts.cronMapEnable || s.cfg.CronMap.Enabled)
	if path == "" {
		return nil
	}

	explicit := s.cfg.CronMap.Path != ""
	// #nosec G304 -- path is the operator-supplied crons.yaml location
	// from service config, not request-derived input.
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !explicit {
			// Default-path file missing is acceptable — lets a
			// service add cron jobs later without forcing day-one
			// presence.
			return nil
		}
		return xerrs.Wrap(err, xerrs.KindValidation, CodeCronMapYAMLNotFound,
			"service: cronmap read yaml "+path)
	}

	eng := cronmap.New(cronmap.WithEnv(s.opts.cronMapEnv))
	if err := eng.LoadBytes(b); err != nil {
		return err
	}
	for name, fn := range s.opts.cronMapHandlers {
		cronmap.RegisterHandler(eng, name, fn)
	}

	// PGLocker wiring: if DB is configured, always pass it — cronmap
	// only consults the Locker when a job actually sets
	// singleton: true. Passing it unconditionally means a YAML
	// edit that flips singleton on does NOT require a redeploy.
	// When DB is absent, cronmap.Build's own validation surfaces
	// CodeSingletonNeedsLocker (wrapped here into the service-side
	// CodeCronMapNeedsDB so dashboards distinguish the two).
	buildOpts := []cronmap.BuildOption{
		cronmap.WithLogger(s.logger),
		cronmap.WithMetrics(s.metrics),
	}
	if s.DB != nil {
		buildOpts = append(buildOpts, cronmap.WithSingletonLocker(cronmap.PGLocker(s.DB)))
	}
	if s.sentryShutdown != nil {
		buildOpts = append(buildOpts, cronmap.WithSentry())
	}

	rt, err := eng.Build(buildOpts...)
	if err != nil {
		// Re-wrap the singleton-needs-locker case into the
		// service-side code so callers searching for
		// "service_cronmap_needs_db" find it.
		if needsLockerErr(err) {
			return xerrs.Wrap(err, xerrs.KindValidation, CodeCronMapNeedsDB,
				"service: cronmap has singleton job but DB is not configured "+
					"(WithSingletonLocker requires *db.DB)")
		}
		return err
	}

	s.CronMap = rt
	if err := rt.Start(s.runCtx); err != nil {
		return err
	}
	s.OnShutdown(func() error {
		// Pass a separate ctx so OnShutdown's LIFO order doesn't
		// race against the runCtx already cancelled at Close head;
		// rt.Stop falls back to its 5s default deadline.
		return rt.Stop(context.Background())
	})
	return nil
}

// needsLockerErr peeks at err's chain for the cronmap-level
// "singleton job present but no locker" code. We re-wrap that into a
// service-prefixed code so the service layer's dashboards / runbooks
// see a consistent service_* error surface.
func needsLockerErr(err error) bool {
	var xe *xerrs.Error
	for {
		if !errors.As(err, &xe) {
			return false
		}
		if xe.Code == cronmap.CodeSingletonNeedsLocker {
			return true
		}
		err = xe.Cause
		if err == nil {
			return false
		}
	}
}
