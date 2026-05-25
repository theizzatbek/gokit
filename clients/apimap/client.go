package apimap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"

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

const maxErrorBodyBytes = 4096 // truncate body included in *errs.Error.Details

// Decode runs endpoint, decodes the response according to endpoint.decode,
// and returns the typed Resp. Non-2xx status maps to *errs.Error with
// Kind derived from the status code.
func Decode[Resp any](ctx context.Context, c *Client, endpoint string, call Call) (Resp, error) {
	var zero Resp
	resp, err := c.Do(ctx, endpoint, call)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	ep := c.endpoints[endpoint]
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		out, derr := decodeInto[Resp](resp, ep.decode)
		if derr != nil {
			return zero, derr
		}
		return out, nil
	}
	return zero, errorForResponse(ep, resp)
}

// Exchange combines encoding the typed request body with Decode for the
// response. The body argument supersedes call.Body.
func Exchange[Req, Resp any](ctx context.Context, c *Client, endpoint string, body Req, call Call) (Resp, error) {
	var zero Resp
	req, err := c.buildRequest(ctx, endpoint, call, body)
	if err != nil {
		return zero, err
	}
	ep, ok := c.endpoints[endpoint]
	if !ok {
		return zero, xerrs.NotFoundf(CodeUnknownEndpoint, "apimap: unknown endpoint %q", endpoint)
	}
	resp, err := ep.httpClient.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		out, derr := decodeInto[Resp](resp, ep.decode)
		if derr != nil {
			return zero, derr
		}
		return out, nil
	}
	return zero, errorForResponse(ep, resp)
}

// decodeInto reads resp.Body per the decode mode and returns the typed
// Resp value.
func decodeInto[Resp any](resp *http.Response, mode string) (Resp, error) {
	var zero Resp
	switch mode {
	case "", "none":
		_, _ = io.Copy(io.Discard, resp.Body)
		return zero, nil
	case "json":
		var out Resp
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return zero, xerrs.Wrap(err, xerrs.KindInternal, CodeDecodeFailed,
				"apimap: json decode failed")
		}
		return out, nil
	case "raw":
		var out Resp
		switch p := any(&out).(type) {
		case *[]byte:
			b, err := io.ReadAll(resp.Body)
			if err != nil {
				return zero, xerrs.Wrap(err, xerrs.KindInternal, CodeDecodeFailed,
					"apimap: raw read failed")
			}
			*p = b
		case *io.ReadCloser:
			*p = resp.Body
		default:
			return zero, xerrs.Validationf(CodeUnsupportedDecodeType,
				"apimap: raw decode requires []byte or io.ReadCloser, got %T", out)
		}
		return out, nil
	}
	return zero, xerrs.Validationf(CodeInvalidDecode,
		"apimap: unsupported decode mode %q", mode)
}

// errorForResponse builds the *errs.Error for a non-2xx response,
// including status/url/body in Details.
func errorForResponse(ep resolvedEndpoint, resp *http.Response) error {
	code := codeForEndpointStatus(ep.clientName, ep.endpointName, resp.StatusCode)
	kind := statusToKind(resp.StatusCode)
	msg := "apimap: " + resp.Status + " from " + ep.clientName + "." + ep.endpointName

	bodySnippet := readBodySnippet(resp)
	return &xerrs.Error{
		Kind:    kind,
		Code:    code,
		Message: msg,
		Details: []xerrs.FieldError{
			{Field: "status", Message: strconv.Itoa(resp.StatusCode)},
			{Field: "url", Message: resp.Request.URL.String()},
			{Field: "body", Message: bodySnippet},
		},
	}
}

// readBodySnippet reads up to maxErrorBodyBytes from resp.Body for
// inclusion in the error Details. Best-effort; failures result in an
// empty snippet.
func readBodySnippet(resp *http.Response) string {
	if resp.Body == nil {
		return ""
	}
	limited := io.LimitReader(resp.Body, maxErrorBodyBytes)
	b, err := io.ReadAll(limited)
	if err != nil {
		return ""
	}
	return string(b)
}
