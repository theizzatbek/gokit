package natsclient

import (
	"context"
	"errors"
	"time"

	"github.com/nats-io/nats.go"

	xerrs "github.com/theizzatbek/gokit/errs"
)

// Request is the typed request/reply (RPC-style) primitive.
//
//	resp, err := natsclient.Request[GetUser, User](
//	    ctx, c, "users.get", GetUser{ID: "42"}, 2*time.Second,
//	)
//
// Encoding goes through the client codec (same as Publish/Subscribe).
// Decoding does likewise. The trace context is propagated onto the
// request headers so the reply side sees a continuation of the
// caller's span — same wire convention as [Publisher].
//
// Returns:
//   - *errs.Error{KindValidation, CodeEncodeFailed}    on request encode failure
//   - *errs.Error{KindTimeout, CodeRequestTimeout}     on no reply within timeout
//   - *errs.Error{KindUnavailable, CodeRequestFailed}  on transport error
//   - *errs.Error{KindValidation, CodeDecodeFailed}    on reply decode failure
func Request[Req, Resp any](ctx context.Context, c *Client, subject string, req Req, timeout time.Duration) (Resp, error) {
	var zero Resp
	body, err := c.opts.codec.Marshal(req)
	if err != nil {
		return zero, xerrs.Wrap(err, xerrs.KindValidation, CodeEncodeFailed, "natsclient: request encode")
	}
	m := &nats.Msg{Subject: subject, Data: body, Header: nats.Header{}}
	headers := map[string][]string{}
	InjectTraceContext(ctx, headers)
	for k, v := range headers {
		m.Header[k] = v
	}
	if m.Header.Get("Content-Type") == "" {
		m.Header.Set("Content-Type", c.opts.codec.ContentType())
	}

	reqCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	reply, err := c.conn.RequestMsgWithContext(reqCtx, m)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, nats.ErrTimeout) || errors.Is(err, nats.ErrNoResponders) {
			return zero, xerrs.Wrap(err, xerrs.KindTimeout, CodeRequestTimeout, "natsclient: request timed out")
		}
		return zero, xerrs.Wrap(err, xerrs.KindUnavailable, CodeRequestFailed, "natsclient: request failed")
	}
	var resp Resp
	if err := c.opts.codec.Unmarshal(reply.Data, &resp); err != nil {
		return zero, xerrs.Wrap(err, xerrs.KindValidation, CodeDecodeFailed, "natsclient: reply decode")
	}
	return resp, nil
}

// ReplyHandler is the typed reply-side handler. Return the typed
// response on success; return an error to send a structured error
// reply (encoded as the codec's representation of the error). The
// kit DOES NOT auto-translate *errs.Error into HTTP-shaped JSON —
// callers wanting that should compose a typed `Result[Resp]` shape
// at the application layer.
type ReplyHandler[Req, Resp any] func(ctx context.Context, req Req) (Resp, error)

// ReplySubscription holds a server-side responder. Drain on shutdown.
type ReplySubscription struct{ sub *nats.Subscription }

// Drain stops accepting requests and waits for in-flight handlers.
func (r *ReplySubscription) Drain() error {
	if r == nil || r.sub == nil {
		return nil
	}
	if err := r.sub.Drain(); err != nil {
		return xerrs.Wrap(err, xerrs.KindUnavailable, "drain_failed", "natsclient: reply drain")
	}
	return nil
}

// Reply subscribes a typed RPC handler to subject. Each request goes
// through the codec, the handler runs, and the response is encoded
// back via the same codec. Trace context is extracted on entry so the
// span continues the caller's chain.
//
// `queueGroup` (empty = no load-balancing) routes one request to one
// member of the group — the canonical pattern for an N-replica
// service.
func Reply[Req, Resp any](
	ctx context.Context,
	c *Client,
	subject string,
	queueGroup string,
	handler ReplyHandler[Req, Resp],
) (*ReplySubscription, error) {
	cb := func(rawMsg *nats.Msg) {
		dispatchCtx := ExtractTraceContext(ctx, map[string][]string(rawMsg.Header))
		var req Req
		if err := c.opts.codec.Unmarshal(rawMsg.Data, &req); err != nil {
			_ = rawMsg.Respond(replyErrorBody(c, err))
			return
		}
		resp, err := handler(dispatchCtx, req)
		if err != nil {
			_ = rawMsg.Respond(replyErrorBody(c, err))
			return
		}
		body, err := c.opts.codec.Marshal(resp)
		if err != nil {
			_ = rawMsg.Respond(replyErrorBody(c, err))
			return
		}
		_ = rawMsg.Respond(body)
	}

	var (
		sub *nats.Subscription
		err error
	)
	if queueGroup != "" {
		sub, err = c.conn.QueueSubscribe(subject, queueGroup, cb)
	} else {
		sub, err = c.conn.Subscribe(subject, cb)
	}
	if err != nil {
		return nil, xerrs.Wrap(err, xerrs.KindUnavailable, CodeConsumerOpFailed, "natsclient: reply subscribe")
	}
	return &ReplySubscription{sub: sub}, nil
}

// replyErrorBody encodes an error using the wire convention
// `{"error": "<err.Error()>"}` (JSONCodec default). Callers needing a
// richer shape should build a typed `Result[Resp]` envelope and
// return the success path with Result.Err populated.
func replyErrorBody(c *Client, err error) []byte {
	body, mErr := c.opts.codec.Marshal(map[string]string{"error": err.Error()})
	if mErr != nil {
		return []byte(`{"error":"encode failed"}`)
	}
	return body
}
