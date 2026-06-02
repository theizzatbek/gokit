package httpc

import (
	"errors"
	"net/http"

	"github.com/theizzatbek/gokit/bulkhead"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// bulkheadTransport caps concurrent requests against the upstream. It
// sits BETWEEN retryTransport and breakerTransport so:
//
//   - Each retry attempt independently Acquires (a retry's backoff
//     sleep does NOT camp on a bulkhead slot).
//   - An open breaker short-circuits BEFORE allocating a slot (the
//     breakerTransport lives below us; if it returns ErrOpen, we never
//     called base, and the deferred release fires on a slot that was
//     never used).
//
// The slot tracks the network round-trip — release fires when the
// wrapped base.RoundTrip returns, BEFORE the caller reads the response
// body. Body-streaming after RoundTrip does NOT hold the slot.
type bulkheadTransport struct {
	base     http.RoundTripper
	bulkhead *bulkhead.Bulkhead
}

func (t *bulkheadTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	release, err := t.bulkhead.Acquire(req.Context())
	if err != nil {
		switch {
		case errors.Is(err, bulkhead.ErrBulkheadFull):
			return nil, xerrs.Wrap(err, xerrs.KindUnavailable,
				CodeBulkheadFull, "httpc: upstream concurrency cap reached")
		case errors.Is(err, bulkhead.ErrQueueTimeout):
			return nil, xerrs.Wrap(err, xerrs.KindTimeout,
				CodeBulkheadQueueTimeout, "httpc: bulkhead queue wait timed out")
		}
		// ctx.Canceled / DeadlineExceeded pass through unchanged.
		return nil, err
	}
	defer release()
	return t.base.RoundTrip(req)
}
