package natsclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// ErrPoison is wrapped by SubscribeRaw handlers to indicate the message
// is unrecoverable — the dispatcher will Term it (no redelivery) rather
// than Nak. Use errors.Is(err, ErrPoison) to test.
var ErrPoison = errors.New("natsclient: poison pill")

// RawMsg is the non-generic delivery surfaced to SubscribeRaw handlers.
// Body is the codec-undecoded payload; metadata mirrors Msg[T].
type RawMsg struct {
	Subject      string
	Body         []byte
	Headers      map[string][]string
	Sequence     uint64
	Redeliveries int
	Reply        string
	Timestamp    time.Time
	raw          *nats.Msg
}

// Raw returns the underlying *nats.Msg.
func (m *RawMsg) Raw() *nats.Msg { return m.raw }

// RawHandler is the non-generic message handler. Returned-error semantics:
//
//	nil                          → Ack
//	errors.Is(err, ErrPoison)    → Term (no redelivery)
//	any other error              → Nak with backoff
type RawHandler func(ctx context.Context, m *RawMsg) error

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

	startPolicy   StartPolicy
	filterSubject string
	queueGroup    string
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

// StartPolicy controls where a fresh consumer starts reading. Implementations
// are sealed — use the constructors below.
type StartPolicy interface{ startPolicy() }

type startNew struct{}
type startAll struct{}
type startFromSeq struct{ seq uint64 }
type startFromTime struct{ t time.Time }

func (startNew) startPolicy()      {}
func (startAll) startPolicy()      {}
func (startFromSeq) startPolicy()  {}
func (startFromTime) startPolicy() {}

// StartNew — only deliver messages published after the consumer starts (default).
func StartNew() StartPolicy { return startNew{} }

// StartAll — replay every message currently in the stream, then go live.
func StartAll() StartPolicy { return startAll{} }

// StartFromSequence — start at a specific stream sequence number.
func StartFromSequence(seq uint64) StartPolicy { return startFromSeq{seq: seq} }

// StartFromTime — start at messages published at or after t.
func StartFromTime(t time.Time) StartPolicy { return startFromTime{t: t} }

// WithStartFrom configures the StartPolicy. Default StartNew.
func WithStartFrom(p StartPolicy) SubscribeOption {
	return func(o *subOptions) { o.startPolicy = p }
}

// WithFilterSubject narrows a subscription on a wildcard stream to a specific
// subject. Empty = no filter.
func WithFilterSubject(s string) SubscribeOption {
	return func(o *subOptions) { o.filterSubject = s }
}

// WithQueueGroup load-balances delivery across subscribers in the same group.
// Empty = no queue group.
func WithQueueGroup(g string) SubscribeOption {
	return func(o *subOptions) { o.queueGroup = g }
}

func defaultBackoff(redeliveries int) time.Duration {
	d := time.Second * (1 << (redeliveries - 1))
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

// SubscribeRaw binds a non-generic handler. Subscribe[T] is built on
// top of this and provides the typed decoding layer.
func SubscribeRaw(
	ctx context.Context,
	c *Client,
	subject string,
	handler RawHandler,
	opts ...SubscribeOption,
) (*Subscription, error) {
	streamName, err := c.js.StreamNameBySubject(subject)
	if err != nil {
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
	if o.filterSubject != "" {
		jsSubOpts = append(jsSubOpts, nats.ConsumerFilterSubjects(o.filterSubject))
	}
	switch p := o.startPolicy.(type) {
	case startAll:
		jsSubOpts = append(jsSubOpts, nats.DeliverAll())
	case startFromSeq:
		jsSubOpts = append(jsSubOpts, nats.StartSequence(p.seq))
	case startFromTime:
		jsSubOpts = append(jsSubOpts, nats.StartTime(p.t))
	default:
		jsSubOpts = append(jsSubOpts, nats.DeliverNew())
	}

	logger := c.opts.logger
	metrics := c.metrics
	slots := make(chan struct{}, o.maxInFlight)

	handlerCB := func(rawMsg *nats.Msg) {
		slots <- struct{}{}
		go func() {
			defer func() { <-slots }()
			dispatchRaw(ctx, logger, metrics, handler, rawMsg, o.backoff)
		}()
	}

	detectConsumerDrift(c.js, logger, streamName, o.durable, o.ackWait, o.maxDeliver, o.filterSubject)

	var natsSub *nats.Subscription
	if o.queueGroup != "" {
		natsSub, err = c.js.QueueSubscribe(subject, o.queueGroup, handlerCB, jsSubOpts...)
	} else {
		natsSub, err = c.js.Subscribe(subject, handlerCB, jsSubOpts...)
	}
	if err != nil {
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeConsumerOpFailed, "natsclient: subscribe")
	}
	return &Subscription{natsSub: natsSub}, nil
}

// Subscribe binds a typed handler to subject. Calls SubscribeRaw with a
// typed shim that uses the client's codec to decode the body into T.
// Decode failure logs at Error level and returns ErrPoison-wrapped err,
// causing SubscribeRaw to Term the message.
func Subscribe[T any](
	ctx context.Context,
	c *Client,
	subject string,
	handler Handler[T],
	opts ...SubscribeOption,
) (*Subscription, error) {
	codec := c.opts.codec
	logger := c.opts.logger
	metrics := c.metrics
	shim := func(ctx context.Context, m *RawMsg) error {
		var data T
		if err := codec.Unmarshal(m.Body, &data); err != nil {
			if logger != nil {
				logger.Error("nats decode failed", "subject", m.Subject, "err", err)
			}
			if metrics != nil {
				metrics.IncHandlerDecodeError(m.Subject)
			}
			return fmt.Errorf("natsclient: decode: %w: %s", ErrPoison, err.Error())
		}
		return handler(ctx, Msg[T]{
			Data:         data,
			Subject:      m.Subject,
			Headers:      m.Headers,
			Sequence:     m.Sequence,
			Redeliveries: m.Redeliveries,
			Reply:        m.Reply,
			Timestamp:    m.Timestamp,
			raw:          m.raw,
		})
	}
	return SubscribeRaw(ctx, c, subject, shim, opts...)
}

// dispatchRaw handles a single delivery: build RawMsg → call handler →
// ack/nak/term based on returned error.
func dispatchRaw(
	ctx context.Context,
	logger *slog.Logger,
	metrics *metricsCollector,
	handler RawHandler,
	rawMsg *nats.Msg,
	backoff func(redeliveries int) time.Duration,
) {
	if metrics != nil {
		metrics.IncInFlight(rawMsg.Subject)
		defer metrics.DecInFlight(rawMsg.Subject)
	}
	msg := &RawMsg{
		Subject: rawMsg.Subject,
		Body:    rawMsg.Data,
		Headers: map[string][]string(rawMsg.Header),
		Reply:   rawMsg.Reply,
		raw:     rawMsg,
	}
	if md, err := rawMsg.Metadata(); err == nil {
		msg.Sequence = md.Sequence.Stream
		msg.Redeliveries = int(md.NumDelivered) - 1
		msg.Timestamp = md.Timestamp
	}
	start := time.Now()
	err := handler(ctx, msg)
	if err == nil {
		if metrics != nil {
			metrics.IncHandlerSuccess(rawMsg.Subject)
			metrics.ObserveHandler(rawMsg.Subject, time.Since(start).Seconds())
		}
		_ = rawMsg.Ack()
		return
	}
	if errors.Is(err, ErrPoison) {
		_ = rawMsg.Term()
		return
	}
	if metrics != nil {
		metrics.IncHandlerError(rawMsg.Subject)
		metrics.ObserveHandler(rawMsg.Subject, time.Since(start).Seconds())
	}
	_ = rawMsg.NakWithDelay(backoff(msg.Redeliveries + 1))
}

// detectConsumerDrift logs a Warn if an existing durable consumer for stream/durable
// has different ackWait/maxDeliver/filterSubject from what Subscribe would create.
// Returns silently if no logger set or no durable consumer exists yet.
func detectConsumerDrift(
	js nats.JetStreamContext,
	logger *slog.Logger,
	stream, durable string,
	wantAckWait time.Duration,
	wantMaxDeliver int,
	wantFilterSubject string,
) {
	if logger == nil || durable == "" {
		return
	}
	ci, err := js.ConsumerInfo(stream, durable)
	if err != nil {
		return // not created yet — Subscribe will create it
	}
	if ci.Config.AckWait != wantAckWait ||
		ci.Config.MaxDeliver != wantMaxDeliver ||
		ci.Config.FilterSubject != wantFilterSubject {
		logger.Warn("nats consumer config drift — kit uses existing; recreate manually if you want kit's values",
			"stream", stream,
			"durable", durable,
			"have_ack_wait", ci.Config.AckWait, "want_ack_wait", wantAckWait,
			"have_max_deliver", ci.Config.MaxDeliver, "want_max_deliver", wantMaxDeliver,
			"have_filter", ci.Config.FilterSubject, "want_filter", wantFilterSubject,
		)
	}
}
