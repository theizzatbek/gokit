package apimap

import (
	"context"
	"net/http"
	"net/url"
	"reflect"

	xerrs "github.com/theizzatbek/fibermap/errs"
)

// Client is the immutable post-Build dispatcher. Goroutine-safe.
type Client struct {
	endpoints map[string]resolvedEndpoint // key = "<client>.<endpoint>"
}

// resolvedEndpoint is the runtime data for one endpoint after Build:
// fully-resolved URL template, merged headers, encoding modes, and the
// *http.Client to send through (shared per-client, or dedicated when
// the endpoint overrides timeout/retry).
type resolvedEndpoint struct {
	clientName   string
	endpointName string
	method       string
	baseURL      string
	pathTemplate string
	pathVars     []string
	defaultHdrs  map[string]string // client-level
	authHdrName  string            // "" if no auth; else the header name
	authHdrValue string            // "" if no auth; else the header value
	endpointHdrs map[string]string // endpoint-level
	encode       string            // "" treated as "none"
	decode       string            // "" treated as "none"
	httpClient   *http.Client
	reqType      reflect.Type // nil if not registered
	respType     reflect.Type // nil if not registered
}

// Do issues the request for endpoint with the values in call and returns
// the stdlib *http.Response. Caller decodes/closes Body.
//
// Non-2xx status is NOT converted to an error — callers wanting status
// mapping should use Decode/Exchange. Do exists as the escape hatch for
// flows that need full control (streaming downloads, custom decoders).
func (c *Client) Do(ctx context.Context, endpoint string, call Call) (*http.Response, error) {
	req, err := c.buildRequest(ctx, endpoint, call, nil)
	if err != nil {
		return nil, err
	}
	ep := c.endpoints[endpoint]
	return ep.httpClient.Do(req)
}

// buildRequest constructs the *http.Request for endpoint applying path
// substitution, query merge, header merge, and body encoding. If
// bodyOverride is non-nil it takes precedence over call.Body (used by
// Exchange to pass the typed request).
func (c *Client) buildRequest(ctx context.Context, endpoint string, call Call, bodyOverride any) (*http.Request, error) {
	ep, ok := c.endpoints[endpoint]
	if !ok {
		return nil, xerrs.NotFoundf(CodeUnknownEndpoint,
			"apimap: unknown endpoint %q", endpoint)
	}

	pathPart, err := substitutePath(ep.pathTemplate, ep.pathVars, call.Path)
	if err != nil {
		return nil, err
	}

	full, err := url.Parse(ep.baseURL + pathPart)
	if err != nil {
		return nil, xerrs.Wrapf(err, xerrs.KindInternal, CodeInvalidBaseURL,
			"apimap: assemble URL for endpoint %q", endpoint)
	}
	if len(call.Query) > 0 {
		q := full.Query()
		for k, vs := range call.Query {
			q[k] = vs
		}
		full.RawQuery = q.Encode()
	}

	body := call.Body
	if bodyOverride != nil {
		body = bodyOverride
	}
	bodyReader, contentType, err := encodeBody(ep.encode, body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, ep.method, full.String(), bodyReader)
	if err != nil {
		return nil, xerrs.Wrapf(err, xerrs.KindInternal, CodeUnknownEndpoint,
			"apimap: build http.Request for endpoint %q", endpoint)
	}

	// Header precedence (last wins): defaults < auth < endpoint < call.
	// Auth slots between defaults and endpoint so endpoint.headers can
	// override auth (rare) and Call.Headers always wins (tests/overrides).
	endpointPlusAuth := ep.endpointHdrs
	if ep.authHdrName != "" {
		merged := make(map[string]string, len(ep.endpointHdrs)+1)
		merged[ep.authHdrName] = ep.authHdrValue
		for k, v := range ep.endpointHdrs {
			merged[k] = v
		}
		endpointPlusAuth = merged
	}
	headers := mergeHeaders(ep.defaultHdrs, endpointPlusAuth, call.Headers)
	for k, vs := range headers {
		req.Header[k] = vs
	}
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}
