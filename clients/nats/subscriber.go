package natsclient

import (
	"context"
	"errors"
	"time"

	"github.com/nats-io/nats.go"

	xerrs "github.com/theizzatbek/fibermap/errs"
)

// Msg is what a Subscribe handler receives — the decoded payload plus JetStream
// metadata. Use Raw() to escape into the underlying *nats.Msg for cases the
// typed API doesn't cover (manual Ack/Nak/Term, custom header parsing).
type Msg[T any] struct {
	Data         T
	Subject      string
	Headers      map[string][]string
	Sequence     uint64
	Redeliveries int
	Reply        string
	Timestamp    time.Time
	raw          *nats.Msg
}

// Raw returns the underlying *nats.Msg.
func (m Msg[T]) Raw() *nats.Msg { return m.raw }

// Handler is the typed message handler. Return nil to Ack, error to Nak (with
// backoff). Decode failures Term the message — they never reach Handler.
type Handler[T any] func(ctx context.Context, msg Msg[T]) error

// Subscription is a live JetStream subscription. Drain on shutdown.
type Subscription struct {
	natsSub *nats.Subscription
}

// Drain stops new deliveries and waits for in-flight handlers to finish.
func (s *Subscription) Drain() error {
	if s == nil || s.natsSub == nil {
		return nil
	}
	if err := s.natsSub.Drain(); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, "drain_failed", "natsclient: drain")
	}
	return nil
}

// Unsubscribe is the immediate version — does NOT wait for in-flight.
func (s *Subscription) Unsubscribe() error {
	if s == nil || s.natsSub == nil {
		return nil
	}
	if err := s.natsSub.Unsubscribe(); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, "unsubscribe_failed", "natsclient: unsubscribe")
	}
	return nil
}

// subOptions is the resolved set of SubscribeOptions. Tasks 13/16 extend this.
type subOptions struct {
	durable string
}

// SubscribeOption configures Subscribe. Tasks 13/16 add the full set.
type SubscribeOption func(*subOptions)

// WithDurable sets the JetStream durable consumer name.
func WithDurable(name string) SubscribeOption {
	return func(o *subOptions) { o.durable = name }
}

// Subscribe binds a typed handler to subject. The subject must belong to a
// stream you EnsureStream'd. Returns a *Subscription — Drain on shutdown.
func Subscribe[T any](
	ctx context.Context,
	c *Client,
	subject string,
	handler Handler[T],
	opts ...SubscribeOption,
) (*Subscription, error) {
	if _, err := c.js.StreamNameBySubject(subject); err != nil {
		if errors.Is(err, nats.ErrStreamNotFound) || errors.Is(err, nats.ErrNoStreamResponse) {
			return nil, xerrs.Wrapf(err, xerrs.KindNotFound, CodeStreamNotFound,
				"natsclient: no stream for subject %q (did you EnsureStream?)", subject)
		}
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeConsumerOpFailed, "natsclient: stream lookup")
	}

	o := subOptions{}
	for _, fn := range opts {
		fn(&o)
	}

	jsSubOpts := []nats.SubOpt{}
	if o.durable != "" {
		jsSubOpts = append(jsSubOpts, nats.Durable(o.durable))
	}
	jsSubOpts = append(jsSubOpts, nats.ManualAck())

	codec := c.opts.codec

	natsSub, err := c.js.Subscribe(subject, func(rawMsg *nats.Msg) {
		dispatchOne(ctx, codec, handler, rawMsg)
	}, jsSubOpts...)
	if err != nil {
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeConsumerOpFailed, "natsclient: subscribe")
	}
	return &Subscription{natsSub: natsSub}, nil
}

// dispatchOne handles a single delivery: decode → call handler → ack/nak/term.
// The full auto-Nak-with-backoff and decode-Term logic lands in Tasks 14/15.
func dispatchOne[T any](
	ctx context.Context,
	codec Codec,
	handler Handler[T],
	rawMsg *nats.Msg,
) {
	var data T
	if err := codec.Unmarshal(rawMsg.Data, &data); err != nil {
		// Task 15 turns this into Term + Error log.
		_ = rawMsg.Term()
		return
	}
	msg := Msg[T]{
		Data:    data,
		Subject: rawMsg.Subject,
		Headers: map[string][]string(rawMsg.Header),
		Reply:   rawMsg.Reply,
		raw:     rawMsg,
	}
	if md, err := rawMsg.Metadata(); err == nil {
		msg.Sequence = md.Sequence.Stream
		msg.Redeliveries = int(md.NumDelivered) - 1
		msg.Timestamp = md.Timestamp
	}
	if err := handler(ctx, msg); err != nil {
		// Task 14 turns this into Nak with backoff.
		_ = rawMsg.Nak()
		return
	}
	_ = rawMsg.Ack()
}

// keep time import alive across early tasks (Task 13/14 use it)
var _ time.Duration
