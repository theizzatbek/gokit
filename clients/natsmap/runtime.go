package natsmap

import (
	"context"
	"reflect"
	"sort"
	"sync"

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

	drainOnce sync.Once
	drainErr  error
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
	if id := reqctx.RequestIDFromContext(ctx); id != "" {
		if _, explicit := headers[reqctx.HeaderRequestID]; !explicit {
			if headers == nil {
				headers = map[string][]string{}
			}
			headers[reqctx.HeaderRequestID] = []string{id}
		}
	}
	merged := mergeHeaders(shim.staticHdrs, headers)
	if err := shim.publishRaw(ctx, payload, merged); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodePublishFailed,
			"natsmap: publish "+name)
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
	if id := reqctx.RequestIDFromContext(ctx); id != "" {
		if _, explicit := headers[reqctx.HeaderRequestID]; !explicit {
			if headers == nil {
				headers = map[string][]string{}
			}
			headers[reqctx.HeaderRequestID] = []string{id}
		}
	}
	merged := mergeHeaders(shim.staticHdrs, headers)
	if err := shim.publish(ctx, payload, merged); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodePublishFailed,
			"natsmap: publish "+name)
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
