package cronmap

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Engine is the build-once configurator. The lifecycle is:
//
//	New → LoadFile/LoadBytes (n) → RegisterHandler (n) → Build (once)
//
// After Build the engine is sealed — further Load*/Register* calls
// panic. Concurrent access during the configuration phase is the
// caller's responsibility; the engine is single-threaded at boot.
type Engine struct {
	jobs     []rawJob
	handlers map[string]HandlerFn
	envMap   map[string]string
	built    bool
}

// New returns an empty engine. Pass [EngineOption] values to
// configure construction-time behaviour.
func New(opts ...EngineOption) *Engine {
	e := &Engine{
		handlers: map[string]HandlerFn{},
	}
	for _, fn := range opts {
		fn(e)
	}
	return e
}

// LoadFile reads a YAML file and appends its jobs to the engine.
// May be called multiple times so different teams can own different
// crons.yaml files; Build validates the union.
func (e *Engine) LoadFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeMissingSchedule,
			"cronmap: read yaml file: "+path)
	}
	return e.LoadBytes(b)
}

// LoadBytes parses and appends YAML content.
func (e *Engine) LoadBytes(b []byte) error {
	if e.built {
		panic(xerrs.Validation(CodeAlreadyBuilt,
			"cronmap: LoadBytes after Build"))
	}
	cfg, err := parseBytes(b, e.envLookup())
	if err != nil {
		return err
	}
	e.jobs = append(e.jobs, cfg.Jobs...)
	return nil
}

// envLookup returns the composite ${VAR} resolver: engine map first,
// then os.LookupEnv. Returns nil when no map is set, letting
// substituteEnv fall back to its default.
func (e *Engine) envLookup() func(string) (string, bool) {
	if e.envMap == nil {
		return nil
	}
	return func(name string) (string, bool) {
		if v, ok := e.envMap[name]; ok {
			return v, true
		}
		return os.LookupEnv(name)
	}
}

// RegisterHandler binds name → fn. Panics with *errs.Error on
// duplicate name or post-Build registration — matches the
// programmer-error convention shared with [fibermap.RegisterHandler].
// Tests that exercise this use `defer recover()`.
func RegisterHandler(e *Engine, name string, fn HandlerFn) {
	if e.built {
		panic(xerrs.Validationf(CodeAlreadyBuilt,
			"cronmap: cannot RegisterHandler %q after Build", name))
	}
	if _, exists := e.handlers[name]; exists {
		panic(xerrs.Validationf(CodeAlreadyRegistered,
			"cronmap: duplicate RegisterHandler for %q", name))
	}
	if fn == nil {
		panic(xerrs.Validationf(CodeAlreadyRegistered,
			"cronmap: RegisterHandler %q called with nil fn", name))
	}
	e.handlers[name] = fn
}

// Build validates every job against the registered handlers, parser,
// locker presence, and uniqueness rules. Returns a runtime ready to
// Start. Build can only be called once per engine.
//
// All validation errors are collected via [errors.Join] so a caller
// sees every problem in one pass instead of fixing them one at a
// time.
func (e *Engine) Build(opts ...BuildOption) (*Runtime, error) {
	if e.built {
		return nil, xerrs.Validation(CodeAlreadyBuilt,
			"cronmap: Engine.Build called twice")
	}

	o := &buildOptions{parser: defaultParser()}
	for _, fn := range opts {
		fn(o)
	}

	var buildErrs []error

	// Per-job validation that doesn't depend on engine state.
	for i := range e.jobs {
		if err := e.jobs[i].validate(i); err != nil {
			buildErrs = append(buildErrs, err)
		}
	}

	// Engine-level cross-checks: duplicate names, unknown handlers,
	// singleton-without-locker, parser errors.
	seen := map[string]struct{}{}
	plan := make([]plannedJob, 0, len(e.jobs))
	needsLocker := false
	for i := range e.jobs {
		j := e.jobs[i]
		if j.Name == "" || j.Handler == "" || j.Schedule == "" {
			// Already flagged by per-job validate; skip planning to
			// avoid noisy follow-on errors.
			continue
		}
		if _, dup := seen[j.Name]; dup {
			buildErrs = append(buildErrs, xerrs.Validationf(CodeDuplicateJob,
				"cronmap: duplicate job name %q", j.Name))
			continue
		}
		seen[j.Name] = struct{}{}

		fn, ok := e.handlers[j.Handler]
		if !ok {
			buildErrs = append(buildErrs, xerrs.Validationf(CodeUnknownHandler,
				"cronmap: job %q references unknown handler %q "+
					"(call RegisterHandler before Build)",
				j.Name, j.Handler))
			continue
		}

		sched, err := o.parser.Parse(j.Schedule)
		if err != nil {
			buildErrs = append(buildErrs, xerrs.Wrapf(err,
				xerrs.KindValidation, CodeInvalidSchedule,
				"cronmap: job %q has invalid schedule %q", j.Name, j.Schedule))
			continue
		}

		if j.Singleton {
			needsLocker = true
		}

		slug := j.SentrySlug
		if slug == "" {
			slug = slugify(j.Name)
		}

		plan = append(plan, plannedJob{
			name:         j.Name,
			handler:      fn,
			schedule:     sched,
			timeout:      j.Timeout,
			singleton:    j.Singleton,
			slug:         slug,
			maxRetries:   j.MaxRetries,
			retryBackoff: j.RetryBackoff,
		})
	}

	if needsLocker && o.locker == nil {
		buildErrs = append(buildErrs, xerrs.Validation(CodeSingletonNeedsLocker,
			"cronmap: at least one job has singleton: true but "+
				"WithSingletonLocker was not passed to Build"))
	}

	if err := errors.Join(buildErrs...); err != nil {
		return nil, err
	}

	e.built = true

	rt := &Runtime{
		jobs:           plan,
		locker:         o.locker,
		logger:         o.logger,
		useSentry:      o.useSentry,
		onTickStart:    o.onTickStart,
		onTickComplete: o.onTickComplete,
		states:         make(map[string]*jobState, len(plan)),
	}
	for i := range plan {
		rt.states[plan[i].name] = &jobState{}
	}
	rt.collectors = newMetricsCollector(o.metrics)
	rt.collectors.setJobs(float64(len(plan)))
	// Cron runtime + lifecycle channels are constructed in Start so a
	// Build-only flow (e.g. validating YAML offline) doesn't spawn a
	// goroutine.
	rt.cronCfg = cron.WithParser(o.parser)
	return rt, nil
}

// plannedJob is the resolved per-job runtime data — built once at
// Build time and read on every tick. Mutable counters live on
// jobState (separate map keyed by name) so plannedJob stays a
// read-only slice across goroutines.
type plannedJob struct {
	name         string
	handler      HandlerFn
	schedule     cron.Schedule
	timeout      time.Duration
	singleton    bool
	slug         string
	maxRetries   int
	retryBackoff time.Duration
}

// slugify turns a human job name ("Daily Rollup") into a
// hyphen-cased Sentry-Crons slug ("daily-rollup"). Used as the
// default sentry_slug when YAML omits it. Mirrors
// service/cron.go's slugify behaviour so a service that migrates
// from WithCron keeps the same Sentry monitor identity.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case r == '-' || r == '_' || r == ' ' || r == '/' || r == '.':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		return "job"
	}
	return out
}
