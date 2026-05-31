package natsclient

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Publisher publishes typed messages of T. Created per type so the codec is
// bound once. Goroutine-safe.
type Publisher[T any] struct {
	c     *Client
	codec Codec
}

// NewPublisher returns a Publisher for T. Inexpensive — safe to make many.
func NewPublisher[T any](c *Client) *Publisher[T] {
	return &Publisher[T]{c: c, codec: c.opts.codec}
}

// Publish sends msg to subject. If the subject is JetStream-managed, publish
// waits for stream-side ack. Otherwise it's fire-and-forget core publish.
func (p *Publisher[T]) Publish(ctx context.Context, subject string, msg T) error {
	return p.PublishWithHeaders(ctx, subject, msg, nil)
}

// PublishWithHeaders is Publish with custom headers.
func (p *Publisher[T]) PublishWithHeaders(ctx context.Context, subject string, msg T, headers map[string][]string) error {
	body, err := p.codec.Marshal(msg)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeEncodeFailed, "natsclient: payload encode")
	}
	m := &nats.Msg{Subject: subject, Data: body, Header: nats.Header{}}
	for k, v := range headers {
		m.Header[k] = v
	}
	if m.Header.Get("Content-Type") == "" {
		m.Header.Set("Content-Type", p.codec.ContentType())
	}
	if m.Header.Get("Nats-Msg-Id") == "" {
		m.Header.Set("Nats-Msg-Id", uuid.NewString())
	}

	start := time.Now()
	if p.c.isJetStreamSubject(subject) {
		if _, err := p.c.js.PublishMsg(m, nats.Context(ctx)); err != nil {
			if p.c.metrics != nil {
				p.c.metrics.IncPublishError(subject)
			}
			return xerrs.Wrap(err, xerrs.KindUnavailable, CodePublishFailed, "natsclient: js publish")
		}
		if p.c.metrics != nil {
			p.c.metrics.ObservePublish(subject, time.Since(start).Seconds())
			p.c.metrics.IncPublishSuccess(subject)
		}
		return nil
	}
	if err := p.c.conn.PublishMsg(m); err != nil {
		if p.c.metrics != nil {
			p.c.metrics.IncPublishError(subject)
		}
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodePublishFailed, "natsclient: core publish")
	}
	if p.c.metrics != nil {
		p.c.metrics.IncPublishSuccess(subject)
	}
	return nil
}

// isJetStreamSubject memoizes per-subject stream-lookup. Negative results
// (no stream) are cached as "" to avoid hammering js.StreamNameBySubject.
func (c *Client) isJetStreamSubject(subject string) bool {
	c.streamCacheMu.RLock()
	if name, ok := c.streamCache[subject]; ok {
		c.streamCacheMu.RUnlock()
		return name != ""
	}
	c.streamCacheMu.RUnlock()
	name, err := c.js.StreamNameBySubject(subject)
	if err != nil && !errors.Is(err, nats.ErrNoStreamResponse) && !errors.Is(err, nats.ErrStreamNotFound) {
		return false
	}
	c.streamCacheMu.Lock()
	c.streamCache[subject] = name
	c.streamCacheMu.Unlock()
	atomic.AddInt64(&streamCacheStats.lookups, 1)
	return name != ""
}

// streamCacheStats is a package-level counter used by metrics.go.
var streamCacheStats struct {
	lookups int64
}

// PublishRaw publishes pre-encoded bytes without running them through
// the client's codec. Use for cases where the payload was already
// encoded upstream — outbox tables, replay tools, schema registries
// — and re-encoding would either fail (bytes are not the codec's
// native shape) or change the wire bytes (sub-millisecond float drift,
// re-ordered JSON keys, …). For typed payloads use [Publisher.Publish]
// or [PublishViaCodec] instead.
//
// Headers, Content-Type fallback, Nats-Msg-Id generation, and
// core-vs-JS subject routing all match PublishViaCodec — only the
// Marshal step is bypassed.
func PublishRaw(ctx context.Context, c *Client, subject string, payload []byte, headers map[string][]string) error {
	return publishBytes(ctx, c, subject, payload, headers)
}

// PublishViaCodec is the non-generic publish entry point used by other
// kit packages (notably natsmap) that need to publish a typed payload
// resolved at runtime via reflection. It uses the client's codec
// (already configured at Connect) and respects core-vs-JS subject
// routing.
func PublishViaCodec(ctx context.Context, c *Client, subject string, payload any, headers map[string][]string) error {
	body, err := c.opts.codec.Marshal(payload)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeEncodeFailed, "natsclient: payload encode")
	}
	return publishBytes(ctx, c, subject, body, headers)
}

// publishBytes is the shared wire-up shared by PublishViaCodec
// (encodes first) and PublishRaw (already encoded). Owns the header
// defaults, JS-vs-core routing decision, and metric accounting.
//
// Trace context propagation: when the caller's ctx carries an active
// OTel span, W3C traceparent + tracestate headers are injected onto
// the msg so consumers extract them on the subscribe side and the
// async boundary stays trace-connected. No-op when no propagator is
// installed.
func publishBytes(ctx context.Context, c *Client, subject string, body []byte, headers map[string][]string) error {
	// Mutate (or build) a copy of the headers map so the caller's slice
	// doesn't get traceparent stamped onto it across multiple Publish
	// calls. Otherwise a single shared headers map would accumulate
	// stale trace IDs between unrelated calls.
	merged := make(map[string][]string, len(headers)+2)
	for k, v := range headers {
		merged[k] = v
	}
	InjectTraceContext(ctx, merged)
	m := &nats.Msg{Subject: subject, Data: body, Header: nats.Header{}}
	for k, v := range merged {
		m.Header[k] = v
	}
	if m.Header.Get("Content-Type") == "" {
		m.Header.Set("Content-Type", c.opts.codec.ContentType())
	}
	if m.Header.Get("Nats-Msg-Id") == "" {
		m.Header.Set("Nats-Msg-Id", uuid.NewString())
	}
	start := time.Now()
	if c.isJetStreamSubject(subject) {
		if _, err := c.js.PublishMsg(m, nats.Context(ctx)); err != nil {
			if c.metrics != nil {
				c.metrics.IncPublishError(subject)
			}
			return xerrs.Wrap(err, xerrs.KindUnavailable, CodePublishFailed, "natsclient: js publish")
		}
		if c.metrics != nil {
			c.metrics.ObservePublish(subject, time.Since(start).Seconds())
			c.metrics.IncPublishSuccess(subject)
		}
		return nil
	}
	if err := c.conn.PublishMsg(m); err != nil {
		if c.metrics != nil {
			c.metrics.IncPublishError(subject)
		}
		return xerrs.Wrap(err, xerrs.KindUnavailable, CodePublishFailed, "natsclient: core publish")
	}
	if c.metrics != nil {
		c.metrics.IncPublishSuccess(subject)
	}
	return nil
}
