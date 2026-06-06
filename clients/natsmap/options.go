package natsmap

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
)

// Option configures Build.
type Option func(*options)

type options struct {
	logger  *slog.Logger
	metrics prometheus.Registerer

	subscribeOpts      []natsclient.SubscribeOption
	defaultPublishHdrs map[string][]string
	beforeDispatch     func(name, subject string)
	afterDispatch      func(name, subject string, err error, elapsed time.Duration)
	beforePublish      func(ctx context.Context, name, subject string, headers map[string][]string)
	afterPublish       func(ctx context.Context, name, subject string, err error, elapsed time.Duration)
}

// WithLogger sets the slog.Logger used for natsmap-level events
// (registration warnings, future hot-reload). Per-subscription
// observability is inherited from the natsclient.Client (logger passed
// at Connect).
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

// WithMetrics enables natsmap-owned Prometheus collectors on reg:
//
//   - natsmap_handlers_total{name,outcome}            success|error|panic
//   - natsmap_handler_duration_seconds{name}          histogram
//   - natsmap_publishes_total{name,outcome}           success|error
//
// nil registry → no collectors built. Subscription-level
// observability stays on clients/nats (`nats_handler_*`,
// `nats_publish_*`); natsmap counters key on the YAML-declared `name`
// (bounded cardinality), which lets dashboards attribute events back
// to declared subscribers/publishers rather than ad-hoc subjects.
func WithMetrics(reg prometheus.Registerer) Option {
	return func(o *options) { o.metrics = reg }
}

// WithSubscribeOptions passes additional [natsclient.SubscribeOption]
// values through to every subscriber built by Engine.Build. Use to
// plug the natsclient handler-resilience pack —
// WithErrorClassifier, WithAckProgress, WithPanicHandler — uniformly
// across every subscriber.
//
// Per-subscriber overrides via [Engine.RegisterSubscriberOptions] are
// appended AFTER the global slice at Build time so subscriber-specific
// options refine the global baseline rather than replace it.
func WithSubscribeOptions(opts ...natsclient.SubscribeOption) Option {
	return func(o *options) { o.subscribeOpts = append(o.subscribeOpts, opts...) }
}

// WithBeforeDispatch fires once per subscriber delivery BEFORE the
// typed handler runs. Receives the kit-stable (name, subject) pair.
// Use for: span attrs, tenant scoping, audit-entry begin.
//
// Multiple calls — last wins.
func WithBeforeDispatch(fn func(name, subject string)) Option {
	return func(o *options) { o.beforeDispatch = fn }
}

// WithAfterDispatch fires once per subscriber delivery AFTER the
// handler returns. Receives the handler's error (nil on success) and
// the elapsed wall time. Use for: audit-entry commit, custom metrics,
// span attrs close.
//
// Multiple calls — last wins.
func WithAfterDispatch(fn func(name, subject string, err error, elapsed time.Duration)) Option {
	return func(o *options) { o.afterDispatch = fn }
}

// WithBeforePublish fires once per Publish / PublishRaw call BEFORE
// the bytes hit the wire. The headers map is the merged final set
// (defaults → publisher static → call). Mutating the map here updates
// the wire headers — use for: stamping `X-Service-Version`, ctx-aware
// tracing headers the kit doesn't manage natively.
//
// Multiple calls — last wins.
func WithBeforePublish(fn func(ctx context.Context, name, subject string, headers map[string][]string)) Option {
	return func(o *options) { o.beforePublish = fn }
}

// WithAfterPublish fires once per Publish / PublishRaw call AFTER
// the underlying natsclient publish returns. Receives the publish
// error (nil on success) and the elapsed wall time. Use for: audit,
// DLQ recording on persistent failure.
//
// Multiple calls — last wins.
func WithAfterPublish(fn func(ctx context.Context, name, subject string, err error, elapsed time.Duration)) Option {
	return func(o *options) { o.afterPublish = fn }
}

// WithDefaultPublishHeaders sets engine-wide default headers merged
// into every Publish / PublishRaw call BEFORE the publisher's static
// YAML headers and BEFORE the per-call headers. Layering: defaults
// → publisher static → call, with last-wins on per-key conflict.
//
//	natsmap.WithDefaultPublishHeaders(map[string][]string{
//	    "X-Service-Version": {appVersion},
//	    "X-Region":          {region},
//	})
//
// Empty map = no defaults (current behaviour). The map is consulted
// at Build time and stored on the Runtime; runtime mutations of the
// caller's original map are not picked up.
func WithDefaultPublishHeaders(h map[string][]string) Option {
	return func(o *options) {
		if len(h) == 0 {
			return
		}
		clone := make(map[string][]string, len(h))
		for k, v := range h {
			clone[k] = append([]string(nil), v...)
		}
		o.defaultPublishHdrs = clone
	}
}
