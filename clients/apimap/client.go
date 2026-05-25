package apimap

import (
	"net/http"
	"reflect"
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
