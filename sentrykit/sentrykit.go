// Package sentrykit is the kit's Sentry error-tracking bootstrap.
//
// One call sets up the Sentry SDK and returns a flush function callers
// register with their cleanup path (service.OnShutdown is the canonical
// home).
//
//	shutdown, err := sentrykit.Setup(ctx, dsn,
//	    sentrykit.WithEnvironment("production"),
//	    sentrykit.WithRelease("1.0.0"),
//	)
//	if err != nil { return err }
//	defer shutdown(context.Background())
//
// Pair with [FiberMiddleware] for per-request hub scoping + panic
// auto-capture, and with [WrapErrorHandler] to surface 5xx responses
// as Sentry events.
//
// service.WithSentry wraps Setup + FiberMiddleware + shutdown wiring
// into a single Service option.
//
// Scope: error tracking only. Performance tracing and metrics belong
// to otelkit (Sentry can ingest those via OTLP if needed).
package sentrykit

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
)

// Option configures [Setup].
type Option func(*config)

type config struct {
	environment      string
	release          string
	sampleRate       float64
	tracesSampleRate float64
	beforeSend       func(*sentry.Event, *sentry.EventHint) *sentry.Event
	debug            bool
	flushTimeout     time.Duration
	serverName       string
	tags             map[string]string
}

// WithEnvironment sets the `environment` tag on every event
// (production, staging, dev, …). Drives the Sentry UI environment
// filter and per-environment alert rules.
func WithEnvironment(env string) Option {
	return func(c *config) { c.environment = env }
}

// WithRelease sets the `release` tag — typically the git SHA, image
// tag, or semver. Lets Sentry attribute errors to the deploy that
// introduced them and surface "regression" markers.
func WithRelease(release string) Option {
	return func(c *config) { c.release = release }
}

// WithSampleRate caps the fraction of error events sent to Sentry
// (0..1). Default 1.0 — every captured event ships. Use < 1.0 for
// high-volume error surfaces (deliberately-noisy validation
// boundaries, third-party SDK warnings, …).
func WithSampleRate(r float64) Option {
	return func(c *config) { c.sampleRate = r }
}

// WithTracesSampleRate sets the fraction of Sentry-native transactions
// sampled. Default 0 — performance tracing belongs to otelkit, which
// can target Sentry's OTLP endpoint when needed. Use > 0 only when
// running Sentry SDK tracing instead of OTel.
func WithTracesSampleRate(r float64) Option {
	return func(c *config) { c.tracesSampleRate = r }
}

// WithBeforeSend installs a hook called before each event ships. Use
// to scrub PII, drop noisy errors, or attach extra context. Returning
// nil drops the event.
func WithBeforeSend(fn func(*sentry.Event, *sentry.EventHint) *sentry.Event) Option {
	return func(c *config) { c.beforeSend = fn }
}

// WithDebug enables Sentry SDK debug logging to stderr. Use during
// local setup to confirm DSN + transport behaviour; never in
// production (SDK debug logs are verbose).
func WithDebug(debug bool) Option {
	return func(c *config) { c.debug = debug }
}

// WithFlushTimeout overrides the default 5s deadline used by the
// returned shutdown function when the supplied context has no
// deadline of its own. Set lower for fast-fail shutdown; higher when
// the Sentry transport is known-slow.
func WithFlushTimeout(d time.Duration) Option {
	return func(c *config) { c.flushTimeout = d }
}

// WithServerName overrides the auto-detected hostname tag.
func WithServerName(name string) Option {
	return func(c *config) { c.serverName = name }
}

// WithTag pre-populates a tag that lands on every event. Useful for
// service.NodeName, region, AZ, cluster — facts the deploy knows but
// Sentry's auto-detection can't infer.
func WithTag(key, value string) Option {
	return func(c *config) {
		if c.tags == nil {
			c.tags = map[string]string{}
		}
		c.tags[key] = value
	}
}

// Setup initializes the process-global Sentry hub via sentry.Init and
// returns a shutdown function callers register with their cleanup
// path. dsn must be non-empty — empty values trip an error so
// misconfigured services fail loud at boot. To skip Sentry without
// touching the call site, don't call Setup at all.
//
// The returned shutdown function flushes pending events. Bound the
// context with a finite deadline before calling it; an unresponsive
// Sentry endpoint otherwise blocks shutdown for the full flush
// timeout. Shutdown is sync.Once-guarded — repeated calls are safe.
//
// Setup is idempotent on the kit side but the global hub is not —
// calling Setup twice re-initialises the SDK. Tests typically call
// Setup with an empty DSN (which is rejected) or supply a test
// transport via WithBeforeSend.
func Setup(ctx context.Context, dsn string, opts ...Option) (func(context.Context) error, error) {
	if dsn == "" {
		return nil, errors.New("sentrykit: dsn is required")
	}
	cfg := &config{
		sampleRate:   1.0,
		flushTimeout: 5 * time.Second,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      cfg.environment,
		Release:          cfg.release,
		SampleRate:       cfg.sampleRate,
		TracesSampleRate: cfg.tracesSampleRate,
		BeforeSend:       cfg.beforeSend,
		Debug:            cfg.debug,
		ServerName:       cfg.serverName,
	}); err != nil {
		return nil, err
	}

	// Pre-populate global-scope tags so every event ships with them.
	if len(cfg.tags) > 0 {
		sentry.ConfigureScope(func(s *sentry.Scope) {
			for k, v := range cfg.tags {
				s.SetTag(k, v)
			}
		})
	}

	var once sync.Once
	timeoutDefault := cfg.flushTimeout
	return func(shutdownCtx context.Context) error {
		once.Do(func() {
			timeout := timeoutDefault
			if dl, ok := shutdownCtx.Deadline(); ok {
				if d := time.Until(dl); d > 0 && d < timeout {
					timeout = d
				}
			}
			sentry.Flush(timeout)
		})
		return nil
	}, nil
}