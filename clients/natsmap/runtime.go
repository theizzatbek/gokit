package natsmap

import (
	"context"
	"reflect"
	"sort"
	"sync"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/reqctx"
)

// Runtime is the post-Build dispatcher. Goroutine-safe.
type Runtime struct {
	subs            []*natsclient.Subscription
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
