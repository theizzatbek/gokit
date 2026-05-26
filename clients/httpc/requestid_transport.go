package httpc

import (
	"net/http"

	"github.com/theizzatbek/gokit/reqctx"
)

// requestIDTransport sets the X-Request-ID header on outbound requests
// from the request context (reqctx.RequestIDFromContext). If the
// request already has X-Request-ID, the explicit value wins.
type requestIDTransport struct {
	base http.RoundTripper
}

func (t *requestIDTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get(reqctx.HeaderRequestID) != "" {
		return t.base.RoundTrip(req)
	}
	id := reqctx.RequestIDFromContext(req.Context())
	if id == "" {
		return t.base.RoundTrip(req)
	}
	// Shallow-clone to avoid mutating the caller's request.
	clone := req.Clone(req.Context())
	clone.Header.Set(reqctx.HeaderRequestID, id)
	return t.base.RoundTrip(clone)
}
