package svckit

import (
	"context"
	"os"

	"github.com/prometheus/client_golang/prometheus"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// New assembles the service. The order mirrors service/build.go, but
// the optional subsystems arrive as mods instead of hard-coded calls.
func New[T any, C any](ctx context.Context, cfg Config, opts ...Option) (*Service[T, C], error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Service.NodeName == "" {
		if hn, err := os.Hostname(); err == nil {
			cfg.Service.NodeName = hn
		}
	}

	o := &options{}
	for _, fn := range opts {
		fn(o)
	}
	if err := validateMods(o.mods); err != nil {
		return nil, err
	}
	applyEnvDefaults(o, cfg)
	warnOrphanedEnv(o)

	logger := o.logger
	if logger == nil {
		logger = newLogger(cfg.Service.LogFormat, cfg.Service.LogLevel,
			cfg.Service.NodeName, cfg.Service.ServerGroup)
	}
	for _, w := range o.bootWarnings {
		logger.Warn("svckit: " + w)
	}
	metrics := o.metrics
	if metrics == nil {
		metrics = prometheus.NewRegistry()
	}

	s := &Service[T, C]{cfg: cfg, logger: logger, metrics: metrics, opts: o, mods: o.mods}
	// #nosec G118 -- runCancel lives on Service and is called once, in Close
	s.runCtx, s.runCancel = context.WithCancel(context.Background())
	s.registerRuntimeCollectors()

	h := &hostImpl{
		opts:        o,
		cfg:         cfg,
		logger:      logger,
		metrics:     metrics,
		nodeName:    cfg.Service.NodeName,
		serverGroup: cfg.Service.ServerGroup,
		ctx:         s.runCtx,
	}

	// Setup phase — before the logger lands on Service and before
	// HTTPC: mods here wrap the logger and add transport.
	for _, m := range o.mods {
		sm, ok := m.(Setuper)
		if !ok {
			continue
		}
		if err := sm.Setup(ctx, h); err != nil {
			s.drainHostShutdowns(o)
			s.Close()
			return nil, wrapModErr(err, m.Name(), CodeModSetupFailed, "setup")
		}
	}
	// Setup may have replaced the logger — pick up the final one.
	s.logger = h.logger

	// An error at any step below must unwind everything that already
	// registered: drain what Setup accumulated on Host now, through
	// the same mutex-protected path the failure branch above uses.
	s.drainHostShutdowns(o)

	if err := s.buildDB(ctx); err != nil {
		s.Close()
		return nil, err
	}
	h.db = s.DB
	if err := s.runMigrations(ctx); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.buildAuth(); err != nil {
		s.Close()
		return nil, err
	}
	h.subjectKey = subjectKeyFn[C](s.Auth)
	if err := s.buildHTTPC(); err != nil {
		s.Close()
		return nil, err
	}
	h.httpc = s.HTTPC

	// Build phase — clients and runtimes.
	for _, m := range o.mods {
		bm, ok := m.(Builder)
		if !ok {
			continue
		}
		err := bm.Build(ctx, h)
		s.drainHostShutdowns(o)
		if err != nil {
			s.Close()
			return nil, wrapModErr(err, m.Name(), CodeModBuildFailed, "build")
		}
	}

	if err := s.buildEngine(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.mountAuthMiddleware(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.registerModFactories(); err != nil {
		s.Close()
		return nil, err
	}

	// Wire phase — the engine is ready.
	for _, m := range o.mods {
		wm, ok := m.(Wirer)
		if !ok {
			continue
		}
		err := wm.Wire(h)
		s.drainHostShutdowns(o)
		if err != nil {
			s.Close()
			return nil, wrapModErr(err, m.Name(), CodeModWireFailed, "wire")
		}
	}
	// A mod is entitled to call Host.RegisterMiddlewareFactory from
	// Wire (v1's rate_limit_redis mounts after the engine is built —
	// squarely a Wire-phase action). registerModFactories is
	// idempotent (see engine_factories.go), so calling it again here
	// only picks up names a mod added during the Wire loop above;
	// names already registered by the first call (right after
	// buildEngine) were deleted from opts.modFactories as they landed.
	if err := s.registerModFactories(); err != nil {
		s.Close()
		return nil, err
	}

	s.collectModStatus()
	s.startRefreshGC()
	if err := s.buildCron(ctx); err != nil {
		s.Close()
		return nil, err
	}
	s.logReady()
	return s, nil
}

// NewSimple is the shorthand for services that need neither their own
// engine payload nor custom claims.
func NewSimple(ctx context.Context, cfg Config, opts ...Option) (*Service[struct{}, struct{}], error) {
	return New[struct{}, struct{}](ctx, cfg, opts...)
}

// drainHostShutdowns moves callbacks a mod registered through Host
// onto Service, so Close tears them down even if the next mod fails.
func (s *Service[T, C]) drainHostShutdowns(o *options) {
	if len(o.shutdownFns) == 0 {
		return
	}
	s.shutdownMu.Lock()
	s.shutdownFns = append(s.shutdownFns, o.shutdownFns...)
	s.shutdownMu.Unlock()
	o.shutdownFns = nil
}

// wrapModErr gives a mod's error a stable core code while keeping the
// mod's own code in the chain: errors.Is and errsval keep working as
// before.
func wrapModErr(err error, modName, code, phase string) error {
	return xerrs.Wrapf(err, xerrs.KindInternal, code,
		"svckit: mod %q failed in %s phase", modName, phase)
}
