package natsmap

import (
	"context"
	"reflect"
	"sort"
	"sync"
	"time"

	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/reqctx"
)

// drainer is the polymorphic shutdown surface for both regular
// (`*natsclient.Subscription`) and batched (`*batchedPuller`)
// subscribers. Drain stops new deliveries / fetches and waits for
// in-flight work to finish.
type drainer interface {
	Drain() error
}

// Runtime is the post-Build dispatcher. Goroutine-safe.
type Runtime struct {
	subs            []drainer
	subscriberNames []string

	publishers map[string]publishShim

	// Mock subscribers — populated via RegisterMockHandler. Routed
	// through DispatchMock; they do NOT receive real NATS deliveries.
	mockHandlers map[string]func(ctx context.Context, ptr any, meta msgMeta) error
	mockTypes    map[string]reflect.Type

	// Engine-wide hooks + observability snapshot. Captured at Build
	// and frozen onto the Runtime; nil = no-op.
	defaultPublishHdrs map[string][]string
	beforeDispatch     func(name, subject string)
	afterDispatch      func(name, subject string, err error, elapsed time.Duration)
	beforePublish      func(ctx context.Context, name, subject string, headers map[string][]string)
	afterPublish       func(ctx context.Context, name, subject string, err error, elapsed time.Duration)
	metrics            *natsmapMetrics

	drainOnce sync.Once
	drainErr  error
}

// DispatchMock fires the mock handler registered under name with the
// supplied typed payload + headers. Intended for unit tests that
// exercise a service's subscriber wiring without NATS. The mock
// handler is invoked synchronously on the caller's goroutine; its
// return error surfaces directly so tests can assert on it.
//
// Returns *errs.Error{Code: "natsmap_unknown_subscriber"} when the
// name is not a mock subscriber. Production code MUST NOT call this.
func DispatchMock[T any](ctx context.Context, r *Runtime, name string, payload T, headers map[string][]string) error {
	fn, ok := r.mockHandlers[name]
	if !ok {
		return xerrs.NotFoundf(CodeUnknownSubscriber,
			"natsmap: no mock handler registered for %q", name)
	}
	want := reflect.TypeOf((*T)(nil)).Elem()
	if regT, regOk := r.mockTypes[name]; regOk && regT != want {
		return xerrs.Validationf(CodePublisherTypeMismatch,
			"natsmap: mock handler %q registered for %s, got %s", name, regT, want)
	}
	ptr := reflect.New(want)
	ptr.Elem().Set(reflect.ValueOf(payload))
	meta := msgMeta{Headers: headers}
	return fn(ctx, ptr.Interface(), meta)
}

// publishShim packages a name-bound publisher with its registered type
// for runtime type-assertion.
type publishShim struct {
	subject     string
	staticHdrs  map[string][]string
	payloadType reflect.Type
	publish     func(ctx context.Context, payload any, hdrs map[string][]string) error
	publishRaw  func(ctx context.Context, body []byte, hdrs map[string][]string) error
}

// Drain stops every subscription gracefully (sub.Drain on each).
// Idempotent — safe to call from defer + main.
func (r *Runtime) Drain() error {
	r.drainOnce.Do(func() {
		var firstErr error
		for _, s := range r.subs {
			if err := s.Drain(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		r.drainErr = firstErr
	})
	return r.drainErr
}

// SubscriberNames returns the registered subscriber names, sorted.
func (r *Runtime) SubscriberNames() []string {
	out := make([]string, len(r.subscriberNames))
	copy(out, r.subscriberNames)
	sort.Strings(out)
	return out
}

// mergePublishHeaders folds engine default headers, the publisher's
// static YAML headers, and the per-call headers into one wire map.
// Layering: defaults → static → call, last wins on per-key conflict.
// X-Request-ID from ctx auto-injects unless any layer already set it.
func (r *Runtime) mergePublishHeaders(ctx context.Context, shim publishShim, callHdrs map[string][]string) map[string][]string {
	out := map[string][]string{}
	for k, v := range r.defaultPublishHdrs {
		out[k] = append([]string(nil), v...)
	}
	for k, v := range shim.staticHdrs {
		out[k] = append([]string(nil), v...)
	}
	for k, v := range callHdrs {
		out[k] = append([]string(nil), v...)
	}
	if id := reqctx.RequestIDFromContext(ctx); id != "" {
		if _, explicit := out[reqctx.HeaderRequestID]; !explicit {
			out[reqctx.HeaderRequestID] = []string{id}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// PublishRaw publishes pre-encoded bytes through the named
// publisher's subject + static headers WITHOUT running the bytes
// through the codec. Use for outbox-style flows where the payload
// was already encoded inside a transaction and the bytes must hit
// the wire unchanged.
//
// Unlike [Publish] / [PublishWithHeaders], PublishRaw does NOT
// type-check against the publisher's registered Go type — by design.
// The caller is responsible for encoding bytes that downstream
// subscribers can decode. Static headers from the YAML publisher
// merge over the per-call ones (per-call wins on key collision),
// and X-Request-ID auto-injects from ctx the same way as the typed
// path.
//
// Returns *errs.Error{Code: "natsmap_unknown_publisher"} for an
// unknown name.
func PublishRaw(ctx context.Context, r *Runtime, name string, payload []byte, headers map[string][]string) error {
	shim, ok := r.publishers[name]
	if !ok {
		return xerrs.NotFoundf(CodeUnknownPublisher,
			"natsmap: unknown publisher %q", name)
	}
	merged := r.mergePublishHeaders(ctx, shim, headers)
	if r.beforePublish != nil {
		r.beforePublish(ctx, name, shim.subject, merged)
	}
	start := time.Now()
	err := shim.publishRaw(ctx, payload, merged)
	if r.afterPublish != nil {
		r.afterPublish(ctx, name, shim.subject, err, time.Since(start))
	}
	if err != nil {
		if r.metrics != nil {
			r.metrics.observePublish(name, "error")
		}
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodePublishFailed,
			"natsmap: publish "+name)
	}
	if r.metrics != nil {
		r.metrics.observePublish(name, "success")
	}
	return nil
}

// PublisherNames returns the registered publisher names, sorted.
func (r *Runtime) PublisherNames() []string {
	out := make([]string, 0, len(r.publishers))
	for name := range r.publishers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Publish sends a typed payload through the named publisher. Returns
// *errs.Error{Code: "natsmap_unknown_publisher"} for unknown name,
// "natsmap_publisher_type_mismatch" if T differs from the registered type.
func Publish[T any](ctx context.Context, r *Runtime, name string, payload T) error {
	return PublishWithHeaders(ctx, r, name, payload, nil)
}

// PublishWithHeaders is Publish with per-call headers (merged over the
// YAML-declared static headers; per-call wins on collision).
// Automatically injects X-Request-ID from ctx unless explicitly supplied.
func PublishWithHeaders[T any](ctx context.Context, r *Runtime, name string,
	payload T, headers map[string][]string) error {
	shim, ok := r.publishers[name]
	if !ok {
		return xerrs.NotFoundf(CodeUnknownPublisher,
			"natsmap: unknown publisher %q", name)
	}
	want := reflect.TypeOf((*T)(nil)).Elem()
	if shim.payloadType != want {
		return xerrs.Validationf(CodePublisherTypeMismatch,
			"natsmap: publisher %q registered for %s, got %s", name, shim.payloadType, want)
	}
	merged := r.mergePublishHeaders(ctx, shim, headers)
	if r.beforePublish != nil {
		r.beforePublish(ctx, name, shim.subject, merged)
	}
	start := time.Now()
	err := shim.publish(ctx, payload, merged)
	if r.afterPublish != nil {
		r.afterPublish(ctx, name, shim.subject, err, time.Since(start))
	}
	if err != nil {
		if r.metrics != nil {
			r.metrics.observePublish(name, "error")
		}
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodePublishFailed,
			"natsmap: publish "+name)
	}
	if r.metrics != nil {
		r.metrics.observePublish(name, "success")
	}
	return nil
}

// mergeHeaders combines static (YAML-declared) and per-call headers.
// Per-call entries overwrite static entries on key collision.
func mergeHeaders(staticHdrs, callHdrs map[string][]string) map[string][]string {
	if len(staticHdrs) == 0 && len(callHdrs) == 0 {
		return nil
	}
	out := make(map[string][]string, len(staticHdrs)+len(callHdrs))
	for k, v := range staticHdrs {
		out[k] = v
	}
	for k, v := range callHdrs {
		out[k] = v
	}
	return out
}
