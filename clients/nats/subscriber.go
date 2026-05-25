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

// subOptions is the resolved set of SubscribeOptions. Task 16 extends this.
type subOptions struct {
	durable     string
	maxInFlight int
	ackWait     time.Duration
	maxDeliver  int
	backoff     func(redeliveries int) time.Duration
}

// SubscribeOption configures Subscribe. Task 16 adds the rest.
type SubscribeOption func(*subOptions)

// WithDurable sets the JetStream durable consumer name.
func WithDurable(name string) SubscribeOption {
	return func(o *subOptions) { o.durable = name }
}

// WithMaxInFlight caps the number of handlers running concurrently. Default 1.
func WithMaxInFlight(n int) SubscribeOption {
	return func(o *subOptions) { o.maxInFlight = n }
}

// WithAckWait sets how long the handler has before NATS redelivers. Default 30s.
func WithAckWait(d time.Duration) SubscribeOption {
	return func(o *subOptions) { o.ackWait = d }
}

// WithMaxDeliver caps total delivery attempts. After this NATS stops
// redelivering. Default 5.
func WithMaxDeliver(n int) SubscribeOption {
	return func(o *subOptions) { o.maxDeliver = n }
}

// WithBackoff overrides the default exponential backoff (1s, 2s, 4s, 8s, 16s,
// capped at 5 min). redeliveries is 1 on first Nak, 2 on second, etc.
func WithBackoff(fn func(redeliveries int) time.Duration) SubscribeOption {
	return func(o *subOptions) { o.backoff = fn }
}

func defaultBackoff(redeliveries int) time.Duration {
	d := time.Second * (1 << (redeliveries - 1))
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
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

	o := subOptions{
		maxInFlight: 1,
		ackWait:     30 * time.Second,
		maxDeliver:  5,
		backoff:     defaultBackoff,
	}
	for _, fn := range opts {
		fn(&o)
	}

	jsSubOpts := []nats.SubOpt{
		nats.ManualAck(),
		nats.AckWait(o.ackWait),
		nats.MaxDeliver(o.maxDeliver),
	}
	if o.durable != "" {
		jsSubOpts = append(jsSubOpts, nats.Durable(o.durable))
	}

	codec := c.opts.codec
	slots := make(chan struct{}, o.maxInFlight)

	natsSub, err := c.js.Subscribe(subject, func(rawMsg *nats.Msg) {
		slots <- struct{}{}
		go func() {
			defer func() { <-slots }()
			dispatchOne(ctx, codec, handler, rawMsg, o.backoff)
		}()
	}, jsSubOpts...)
	if err != nil {
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeConsumerOpFailed, "natsclient: subscribe")
	}
	return &Subscription{natsSub: natsSub}, nil
}

// dispatchOne handles a single delivery: decode → call handler → ack/nak/term.
// The decode-Term logic lands in Task 15.
func dispatchOne[T any](
	ctx context.Context,
	codec Codec,
	handler Handler[T],
	rawMsg *nats.Msg,
	backoff func(redeliveries int) time.Duration,
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
		_ = rawMsg.NakWithDelay(backoff(msg.Redeliveries + 1))
		return
	}
	_ = rawMsg.Ack()
}
