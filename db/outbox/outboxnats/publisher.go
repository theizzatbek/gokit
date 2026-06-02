package outboxnats

import (
	"context"

	"github.com/theizzatbek/gokit/clients/natsmap"
	"github.com/theizzatbek/gokit/db/outbox"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// CodeEmptyPublisherName is the error Code returned when the
// publisher-name resolver yields an empty string (the default resolver
// passes EventType through verbatim, so an empty EventType is the
// usual cause — but a caller-supplied resolver may also produce it).
const CodeEmptyPublisherName = "outboxnats_empty_publisher_name"

// Option tunes [NewPublisher].
type Option func(*options)

type options struct {
	publisherName func(outbox.Event) string
}

// WithPublisherNameFn overrides how an [outbox.Event] maps onto a
// natsmap publisher name. Default: e.EventType (1:1 mapping).
//
// Use this when the YAML publisher names are namespaced or otherwise
// derived from the event:
//
//	outboxnats.NewPublisher(rt,
//	    outboxnats.WithPublisherNameFn(func(e outbox.Event) string {
//	        return "bus." + e.EventType
//	    }),
//	)
func WithPublisherNameFn(fn func(outbox.Event) string) Option {
	return func(o *options) { o.publisherName = fn }
}

// NewPublisher returns an [outbox.PublishFn] that dispatches every
// Event through rt via [natsmap.PublishRaw]. Payload bytes and headers
// pass through unchanged — the outbox row is the source of truth.
//
// natsmap-side errors (unknown publisher, publish-failed) propagate
// as *errs.Error from natsmap; the outbox worker treats any non-nil
// return as a transient failure and reschedules the row per its
// configured backoff. An empty resolved publisher name short-circuits
// the publish with *errs.Error{Code: [CodeEmptyPublisherName]}
// before touching the bus.
func NewPublisher(rt *natsmap.Runtime, opts ...Option) outbox.PublishFn {
	o := &options{
		publisherName: defaultPublisherName,
	}
	for _, opt := range opts {
		opt(o)
	}
	return func(ctx context.Context, e outbox.Event) error {
		name := o.publisherName(e)
		if name == "" {
			return xerrs.Validationf(CodeEmptyPublisherName,
				"outboxnats: empty publisher name for event %s (type %q)",
				e.ID, e.EventType)
		}
		return natsmap.PublishRaw(ctx, rt, name, e.Payload, e.Headers)
	}
}

// defaultPublisherName is the default Event → publisher-name
// resolver: pass EventType through verbatim.
func defaultPublisherName(e outbox.Event) string { return e.EventType }
