package httpc

import (
	"context"
	"errors"
	"net/http"

	"github.com/theizzatbek/gokit/breaker"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// breakerTransport is the kit's circuit-breaker RoundTripper. It sits
// between retryTransport and base in NewTransport's chain so each
// retry attempt independently consults the breaker; the retry loop
// recognises ErrOpen and bails without backoff.
//
// failureFn classifies (resp, err) as a failure for breaker
// bookkeeping. A nil failureFn falls back to defaultBreakerFailure.
type breakerTransport struct {
	base      http.RoundTripper
	breaker   *breaker.Breaker
	failureFn func(*http.Response, error) bool
}

// defaultBreakerFailure is the kit's failure classifier for HTTP
// responses. It mirrors retryTransport's transient-status set, plus
// the rule that user cancellation does NOT charge the upstream's
// failure budget (a browser back-button must not flap the breaker).
//
// DeadlineExceeded IS a failure — that is exactly the "upstream is
// slow" symptom we want to short-circuit.
func defaultBreakerFailure(resp *http.Response, err error) bool {
	if err != nil {
		return !errors.Is(err, context.Canceled)
	}
	if resp == nil {
		return true
	}
	switch resp.StatusCode {
	case 408, 429, 500, 502, 503, 504:
		return true
	}
	return false
}

func (t *breakerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	allowed, done := t.breaker.Allow()
	if !allowed {
		return nil, xerrs.Wrap(breaker.ErrOpen, xerrs.KindUnavailable,
			CodeCircuitOpen, "httpc: upstream unavailable (circuit open)")
	}
	resp, err := t.base.RoundTrip(req)
	fail := t.failureFn
	if fail == nil {
		fail = defaultBreakerFailure
	}
	done(!fail(resp, err))
	return resp, err
}
