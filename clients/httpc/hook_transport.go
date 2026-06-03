package httpc

import (
	"net/http"
	"time"
)

// hookTransport bridges WithBeforeRequest / WithAfterResponse callbacks
// into the RoundTripper chain. Sits ABOVE the user middleware so:
//
//   - beforeRequest sees the request BEFORE any user middleware mutates it
//   - afterResponse sees the final (response, error) AFTER retry +
//     middleware processing, with the elapsed wall time spanning the
//     whole chain.
//
// Either hook may be nil — only the non-nil one fires.
type hookTransport struct {
	base   http.RoundTripper
	before func(*http.Request)
	after  func(*http.Request, *http.Response, error, time.Duration)
}

func (t *hookTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.before != nil {
		t.before(req)
	}
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	if t.after != nil {
		t.after(req, resp, err, time.Since(start))
	}
	return resp, err
}
