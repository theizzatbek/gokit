package natsmap

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	natsclient "github.com/theizzatbek/gokit/clients/nats"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// defaultBatchInterval is applied when a subscriber declares
// batch_size but no batch_interval. Matches the kit-wide
// "batch every second" convention used by batch.Aggregator.
const defaultBatchInterval = time.Second

// batchedPuller is the runtime object for a batched subscriber. It
// owns a JetStream PullSubscribe handle plus a goroutine that loops:
//
//	msgs := sub.Fetch(BatchSize, MaxWait=BatchInterval)
//	decode each → []*T
//	call batched handler with the typed slice
//	on nil → Ack every msg
//	on err → Nak every msg (JetStream redelivers the whole batch)
//
// Implements drainer so Runtime.Drain stops the fetch loop and
// unsubscribes cleanly.
type batchedPuller struct {
	sub      *nats.Subscription
	stop     chan struct{}
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// Drain signals the fetch goroutine to stop, waits for the
// in-flight batch to finish, then unsubscribes. Idempotent.
func (p *batchedPuller) Drain() error {
	if p == nil {
		return nil
	}
	p.stopOnce.Do(func() { close(p.stop) })
	p.wg.Wait()
	if p.sub != nil {
		// Unsubscribe (not Drain — pull subs don't need queue drain).
		_ = p.sub.Unsubscribe()
	}
	return nil
}

// openBatchedSubscriber creates the JetStream Pull subscription and
// spawns the fetch loop. Returns a drainer that the Runtime tears
// down via Drain.
func openBatchedSubscriber(
	ctx context.Context,
	client *natsclient.Client,
	s *rawSubscriber,
	durable string,
	handlerType reflect.Type,
	codec natsclient.Codec,
	batchedFn func(ctx context.Context, ptrs []any, metas []msgMeta) error,
) (drainer, error) {
	if durable == "" {
		// PullSubscribe requires a durable consumer name. The
		// resolver auto-derives durable from the subscriber name
		// when YAML leaves it blank — defensive belt and suspenders.
		durable = s.Name
	}
	interval := s.BatchInterval
	if interval <= 0 {
		interval = defaultBatchInterval
	}

	js := client.JetStream()
	if js == nil {
		return nil, xerrs.Unavailable(CodeSubscribeFailed,
			"natsmap: client has no JetStream context")
	}

	natsSub, err := js.PullSubscribe(s.Subject, durable)
	if err != nil {
		return nil, xerrs.Wrapf(err, xerrs.KindUnavailable, CodeSubscribeFailed,
			"natsmap: PullSubscribe %q on %q", s.Name, s.Subject)
	}

	p := &batchedPuller{
		sub:  natsSub,
		stop: make(chan struct{}),
	}
	p.wg.Add(1)
	// #nosec G118 -- fetchLoop is a long-lived background worker whose
	// lifecycle is the p.stop channel (closed by Stop), not a
	// request-scoped ctx; it dispatches each batch with a fresh
	// background context by design.
	go p.fetchLoop(s, handlerType, codec, interval, batchedFn)

	_ = ctx // reserved for future cancellation propagation
	return p, nil
}

// fetchLoop is the main goroutine. Loops Fetch with the configured
// batch size + interval; dispatches each non-empty batch through the
// batched handler shim; Acks all on success, Naks all on failure.
func (p *batchedPuller) fetchLoop(
	s *rawSubscriber,
	handlerType reflect.Type,
	codec natsclient.Codec,
	interval time.Duration,
	batchedFn func(ctx context.Context, ptrs []any, metas []msgMeta) error,
) {
	defer p.wg.Done()
	for {
		select {
		case <-p.stop:
			return
		default:
		}

		msgs, err := p.sub.Fetch(s.BatchSize, nats.MaxWait(interval))
		if err != nil {
			// Timeout with no messages is the common path — Fetch
			// returns nats.ErrTimeout. Keep looping.
			if errors.Is(err, nats.ErrTimeout) {
				continue
			}
			// Subscription gone (drained): exit the loop.
			if errors.Is(err, nats.ErrConnectionClosed) ||
				errors.Is(err, nats.ErrBadSubscription) {
				return
			}
			// Other errors: brief backoff to avoid hot-looping on a
			// transient JetStream issue.
			select {
			case <-p.stop:
				return
			case <-time.After(200 * time.Millisecond):
				continue
			}
		}
		if len(msgs) == 0 {
			continue
		}

		ptrs, metas, decodeErrs := decodeBatch(msgs, handlerType, codec)
		// Term decode-failed messages so JetStream stops redelivering
		// poison pills. They're excluded from the live batch.
		for i, derr := range decodeErrs {
			if derr != nil {
				_ = msgs[i].Term()
			}
		}
		if len(ptrs) == 0 {
			continue
		}

		// Dispatch under a fresh background context — JetStream
		// fetches have no incoming ctx, and the handler is a long-
		// running domain operation that shouldn't be tied to the
		// fetch deadline.
		// Background context by design: the puller is a long-lived worker
		// driven by p.stop, not a request-scoped ctx (see fetchLoop launch).
		err = batchedFn(context.Background(), ptrs, metas)
		if err == nil {
			ackAll(msgs, decodeErrs)
		} else {
			nakAll(msgs, decodeErrs)
		}
	}
}

// decodeBatch walks msgs and decodes the payload into a *T pointer
// for each. Returns parallel slices of pointers, metas, and per-msg
// decode errors. Successfully-decoded entries get a non-nil ptr +
// meta with decodeErrs[i]==nil; failures leave ptr/meta empty and
// decodeErrs[i] populated.
func decodeBatch(
	msgs []*nats.Msg,
	handlerType reflect.Type,
	codec natsclient.Codec,
) (ptrs []any, metas []msgMeta, decodeErrs []error) {
	ptrs = make([]any, 0, len(msgs))
	metas = make([]msgMeta, 0, len(msgs))
	decodeErrs = make([]error, len(msgs))

	for i, m := range msgs {
		ptr := reflect.New(handlerType).Interface()
		if err := codec.Unmarshal(m.Data, ptr); err != nil {
			decodeErrs[i] = err
			continue
		}
		meta := msgMeta{
			Subject: m.Subject,
			Headers: m.Header,
			Reply:   m.Reply,
		}
		if md, err := m.Metadata(); err == nil && md != nil {
			meta.Sequence = md.Sequence.Stream
			meta.Redeliveries = int(md.NumDelivered) - 1
			meta.Timestamp = md.Timestamp
		}
		ptrs = append(ptrs, ptr)
		metas = append(metas, meta)
	}
	return ptrs, metas, decodeErrs
}

// ackAll acks every successfully-decoded message in the batch.
// Decode-failed positions were already Term'd in the loop.
func ackAll(msgs []*nats.Msg, decodeErrs []error) {
	for i, m := range msgs {
		if decodeErrs[i] != nil {
			continue
		}
		_ = m.Ack()
	}
}

// nakAll naks every successfully-decoded message; JetStream
// redelivers the whole batch on the next fetch (modulo decode
// failures which were Term'd).
func nakAll(msgs []*nats.Msg, decodeErrs []error) {
	for i, m := range msgs {
		if decodeErrs[i] != nil {
			continue
		}
		_ = m.Nak()
	}
}
