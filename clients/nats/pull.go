package natsclient

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// PullSubscription is the typed pull-mode consumer. Use for
// cron-style or batch-style consumption where the caller decides
// WHEN to fetch a batch (vs. push-mode where NATS decides). Best for:
//
//   - "Drain whatever's queued at 02:00 and exit" batch jobs.
//   - Backpressure-sensitive workers that want explicit fetch sizing.
//   - Pipelines that fan-in from multiple subjects under one
//     coordinator goroutine.
//
// Differences from [Subscribe]:
//
//   - Requires Durable (Pull consumers must be durable per
//     JetStream contract).
//   - You call Fetch yourself; messages don't push.
//   - Ack/Nak/Term semantics are identical and run through the same
//     classifier as the push path.
type PullSubscription[T any] struct {
	c          *Client
	sub        *nats.Subscription
	codec      Codec
	classifier func(error) AckAction
	backoff    func(int) time.Duration
	logger     loggerLike
	subject    string
}

// loggerLike is an unexported indirection so the package-internal
// dispatch code can stay log-agnostic; concrete *slog.Logger satisfies it.
type loggerLike interface {
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// NewPullSubscription opens a pull-mode JetStream subscription against
// subject. Durable is required — pass [WithDurable]. The same option
// set as [Subscribe] is honoured (WithMaxDeliver, WithAckWait,
// WithErrorClassifier, WithBackoff, etc); the only one that does
// nothing in pull mode is WithMaxInFlight — the caller controls
// concurrency by sizing each Fetch.
func NewPullSubscription[T any](c *Client, subject string, opts ...SubscribeOption) (*PullSubscription[T], error) {
	streamName, err := c.js.StreamNameBySubject(subject)
	if err != nil {
		if errors.Is(err, nats.ErrStreamNotFound) || errors.Is(err, nats.ErrNoStreamResponse) {
			return nil, xerrs.Wrapf(err, xerrs.KindNotFound, CodeStreamNotFound,
				"natsclient: no stream for subject %q (did you EnsureStream?)", subject)
		}
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeConsumerOpFailed, "natsclient: stream lookup")
	}
	_ = streamName

	o := subOptions{
		ackWait:    30 * time.Second,
		maxDeliver: 5,
		backoff:    defaultBackoff,
		classifier: defaultClassifier,
	}
	for _, fn := range opts {
		fn(&o)
	}
	if o.durable == "" {
		return nil, xerrs.Validation(CodeConsumerOpFailed,
			"natsclient: PullSubscription requires WithDurable")
	}

	jsSubOpts := []nats.SubOpt{
		nats.ManualAck(),
		nats.AckWait(o.ackWait),
		nats.MaxDeliver(o.maxDeliver),
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
	}

	sub, err := c.js.PullSubscribe(subject, o.durable, jsSubOpts...)
	if err != nil {
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeConsumerOpFailed, "natsclient: pull subscribe")
	}
	return &PullSubscription[T]{
		c:          c,
		sub:        sub,
		codec:      c.opts.codec,
		classifier: o.classifier,
		backoff:    o.backoff,
		logger:     loggerOrNop(c.opts.logger),
		subject:    subject,
	}, nil
}

// loggerOrNop bridges nil → noop logger so the dispatch path stays
// branch-free in the hot loop.
func loggerOrNop(l loggerLike) loggerLike {
	if l == nil {
		return nopLogger{}
	}
	return l
}

type nopLogger struct{}

func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

// PendingMsg is one in-flight, manually-acked message returned by Fetch.
// The caller MUST eventually call Ack, Nak, or Term — leaving messages
// dangling holds slots and eventually expires AckWait + redelivers.
type PendingMsg[T any] struct {
	Msg[T]
	subRef *PullSubscription[T]
	done   bool
}

// Ack accepts the message — no redelivery.
func (p *PendingMsg[T]) Ack() error {
	if p.done {
		return nil
	}
	p.done = true
	return p.raw.Ack()
}

// Nak rejects the message with the classifier's default backoff for
// this redelivery count. NATS will redeliver per MaxDeliver.
func (p *PendingMsg[T]) Nak() error {
	if p.done {
		return nil
	}
	p.done = true
	return p.raw.NakWithDelay(p.subRef.backoff(p.Redeliveries + 1))
}

// Term marks the message permanently failed — no further redelivery.
// Use for poison-pill bodies the handler will never accept.
func (p *PendingMsg[T]) Term() error {
	if p.done {
		return nil
	}
	p.done = true
	return p.raw.Term()
}

// Fetch pulls up to batchSize messages, waiting up to maxWait for at
// least one. Returns the decoded batch + a non-nil error only on
// transport failure or empty-batch timeout.
//
// Decode failures are NOT returned in the slice — instead the offending
// message is Term'd (poison-pill suppression) and logged at Error.
// The remaining successful decodes still come through.
//
// Caller MUST iterate the slice and call Ack/Nak/Term on each entry —
// pull-mode does not auto-ack.
func (s *PullSubscription[T]) Fetch(ctx context.Context, batchSize int, maxWait time.Duration) ([]*PendingMsg[T], error) {
	if batchSize <= 0 {
		return nil, xerrs.Validation(CodeConsumerOpFailed, "natsclient: Fetch batchSize must be > 0")
	}
	fetchCtx := ctx
	if maxWait > 0 {
		var cancel context.CancelFunc
		fetchCtx, cancel = context.WithTimeout(ctx, maxWait)
		defer cancel()
	}
	rawMsgs, err := s.sub.Fetch(batchSize, nats.Context(fetchCtx))
	if err != nil {
		if errors.Is(err, nats.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
			return nil, nil
		}
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeConsumerOpFailed, "natsclient: pull fetch")
	}
	out := make([]*PendingMsg[T], 0, len(rawMsgs))
	for _, rm := range rawMsgs {
		var data T
		if derr := s.codec.Unmarshal(rm.Data, &data); derr != nil {
			s.logger.Error("nats pull decode failed",
				"subject", rm.Subject, "err", derr)
			if s.c.metrics != nil {
				s.c.metrics.IncHandlerDecodeError(rm.Subject)
			}
			_ = rm.Term()
			continue
		}
		msg := Msg[T]{
			Data:    data,
			Subject: rm.Subject,
			Headers: map[string][]string(rm.Header),
			Reply:   rm.Reply,
			raw:     rm,
		}
		if md, err := rm.Metadata(); err == nil {
			msg.Sequence = md.Sequence.Stream
			msg.Redeliveries = int(md.NumDelivered) - 1
			msg.Timestamp = md.Timestamp
		}
		out = append(out, &PendingMsg[T]{Msg: msg, subRef: s})
	}
	return out, nil
}

// Run blocks the calling goroutine in a Fetch → dispatch loop until
// ctx is cancelled. Each message goes through the configured
// classifier; the returned error from handler routes to Ack/Nak/Term
// using the same rules as push-mode [Subscribe].
//
// Use Run when you want the cleanest "drain forever" loop without
// writing the batch-iter yourself. Use [Fetch] when you need explicit
// control (e.g. coordinator that interleaves multiple PullSubscriptions
// under one fan-in goroutine).
func (s *PullSubscription[T]) Run(ctx context.Context, handler Handler[T], batchSize int, maxWait time.Duration) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		batch, err := s.Fetch(ctx, batchSize, maxWait)
		if err != nil {
			return err
		}
		for _, p := range batch {
			err := handler(ctx, p.Msg)
			action := s.classifier(err)
			switch action {
			case AckActAck:
				_ = p.Ack()
			case AckActTerm:
				_ = p.Term()
			default:
				_ = p.Nak()
			}
		}
	}
}

// Drain stops the underlying subscription and waits for in-flight
// fetches to finish.
func (s *PullSubscription[T]) Drain() error {
	if s == nil || s.sub == nil {
		return nil
	}
	if err := s.sub.Drain(); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, "drain_failed", "natsclient: pull drain")
	}
	return nil
}

// guard so fmt is referenced even if tests pull the file in isolation.
var _ = fmt.Sprintf
