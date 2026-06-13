package apimap

import (
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/theizzatbek/gokit/breaker"
	"github.com/theizzatbek/gokit/bulkhead"
	"github.com/theizzatbek/gokit/clients/httpc"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Engine is the build-once configurator. New → LoadFile/LoadBytes (n) →
// RegisterRequest/RegisterResponse/RegisterAuth (n) → Build (once).
type Engine struct {
	clients []rawClient

	reqTypes  map[string]reflect.Type              // endpoint full-name → registered request type
	respTypes map[string]reflect.Type              // endpoint full-name → registered response type
	authFns   map[string]func(*http.Request) error // signer id → request-mutating function

	envMap map[string]string // nil = no overrides; LoadBytes builds composite lookup

	// Per-client overrides. Both buffer entries by clientName so the
	// engine can validate-and-apply them at Build time. Build fails
	// loudly if the name does not exist in YAML, so a renamed client
	// surfaces immediately rather than as silently-ignored config.
	clientHTTPCOptions map[string][]httpc.Option
	clientTransports   map[string]http.RoundTripper
	clientDefaultCalls map[string]Call

	built bool
}

// EngineOption configures Engine at construction time.
type EngineOption func(*Engine)

// WithEnv supplies explicit values for ${VAR} substitution at
// LoadBytes/LoadFile time. The map is consulted first; on miss the
// lookup falls back to os.LookupEnv. Pass to New(...). nil or empty
// map is a no-op (falls back to os.LookupEnv for every key).
func WithEnv(m map[string]string) EngineOption {
	return func(e *Engine) { e.envMap = m }
}

// New returns an empty Engine. Pass EngineOption values (e.g.
// WithEnv) to configure.
func New(opts ...EngineOption) *Engine {
	e := &Engine{
		reqTypes:           map[string]reflect.Type{},
		respTypes:          map[string]reflect.Type{},
		authFns:            map[string]func(*http.Request) error{},
		clientHTTPCOptions: map[string][]httpc.Option{},
		clientTransports:   map[string]http.RoundTripper{},
		clientDefaultCalls: map[string]Call{},
	}
	for _, fn := range opts {
		fn(e)
	}
	return e
}

// RegisterClientOptions records per-client [httpc.Option] values that
// apply ONLY to the *http.Client built for the named client (and to
// its endpoint-override clients — same upstream, same options).
// Merged AFTER any engine-wide [WithHTTPCOptions] so client-specific
// options refine rather than replace the global baseline.
//
// Panics with *errs.Error on post-Build registration; Build fails
// with apimap_unknown_client when the name does not exist in YAML.
func (e *Engine) RegisterClientOptions(clientName string, opts ...httpc.Option) {
	if e.built {
		panic(xerrs.Validationf(CodeAlreadyBuilt,
			"apimap: cannot RegisterClientOptions %q after Build", clientName))
	}
	e.clientHTTPCOptions[clientName] = append(e.clientHTTPCOptions[clientName], opts...)
}

// RegisterTransport replaces the per-client http.RoundTripper at
// Build with the supplied transport. Use for unit-test mocks
// (httptest.NewServer, gock, custom recorder) — production code
// should not call this. Build fails with apimap_unknown_client when
// the name does not exist in YAML.
//
// When set, the breaker / bulkhead / retry chain still wraps the
// supplied RoundTripper exactly as it would wrap http.DefaultTransport
// — your mock still goes through retry + observability. Pass
// http.DefaultTransport to no-op the override.
func (e *Engine) RegisterTransport(clientName string, rt http.RoundTripper) {
	if e.built {
		panic(xerrs.Validationf(CodeAlreadyBuilt,
			"apimap: cannot RegisterTransport %q after Build", clientName))
	}
	if rt == nil {
		delete(e.clientTransports, clientName)
		return
	}
	e.clientTransports[clientName] = rt
}

// SetClientDefaultCall records a per-client default Call merged into
// every Do/Decode/Exchange that routes to that client BEFORE the
// caller's Call (caller wins on conflict). Layered ABOVE engine-wide
// [WithDefaultCall] so client-specific defaults can refine the
// global baseline.
//
// Panics on post-Build registration.
func (e *Engine) SetClientDefaultCall(clientName string, c Call) {
	if e.built {
		panic(xerrs.Validationf(CodeAlreadyBuilt,
			"apimap: cannot SetClientDefaultCall %q after Build", clientName))
	}
	e.clientDefaultCalls[clientName] = c
}

// LoadFile reads a YAML file and appends its clients to the engine.
// May be called multiple times (one file per upstream API is the
// expected pattern).
func (e *Engine) LoadFile(path string) error {
	// #nosec G304 -- path is the operator-supplied YAML spec location
	// passed at boot, not request-derived input.
	b, err := os.ReadFile(path)
	if err != nil {
		return xerrs.Wrap(err, xerrs.KindValidation, CodeInvalidBaseURL,
			"apimap: read yaml file: "+path)
	}
	return e.LoadBytes(b)
}

// LoadBytes parses and appends YAML content.
func (e *Engine) LoadBytes(b []byte) error {
	cfg, err := parseBytes(b, e.envLookup())
	if err != nil {
		return err
	}
	e.clients = append(e.clients, cfg.Clients...)
	return nil
}

// envLookup returns the composite ${VAR} resolver: engine map first,
// then os.LookupEnv. Returns nil when no map is set, letting
// substituteEnv use its default (os.LookupEnv).
func (e *Engine) envLookup() func(string) (string, bool) {
	if e.envMap == nil {
		return nil
	}
	return func(name string) (string, bool) {
		if v, ok := e.envMap[name]; ok {
			return v, true
		}
		return os.LookupEnv(name)
	}
}

// RegisterRequest records the Go type Exchange must use as Req for the
// given endpoint. At call time Exchange compares the generic Req's
// runtime type to the registered one and panics with
// *errs.Error{Code: CodeTypeMismatch} on disagreement — typed-call drift
// (e.g. Exchange[UpdatedReq] after a model rename) surfaces immediately
// instead of as a silent JSON decode shape change.
//
// Build validates that every registered endpoint name exists in the
// YAML; unknown names fail Build with apimap_registered_endpoint_missing.
//
// Panics with *errs.Error on duplicate registration or post-Build
// registration (programmer error at startup; same convention as
// fibermap's RegisterHandler).
func RegisterRequest[T any](e *Engine, endpoint string) {
	registerType(e, endpoint, e.reqTypes, reflect.TypeOf((*T)(nil)).Elem(), "request")
}

// RegisterResponse records the Go type Decode/Exchange must use as Resp
// for the given endpoint. Same runtime check + panic semantics as
// RegisterRequest.
func RegisterResponse[T any](e *Engine, endpoint string) {
	registerType(e, endpoint, e.respTypes, reflect.TypeOf((*T)(nil)).Elem(), "response")
}

// RegisterAuth registers a request-mutating function under name. YAML
// clients declaring `auth: { type: custom, name: <name> }` resolve at
// Build time to this function. Typical use is HMAC / sign-with-secret
// integrations where the signature depends on per-request method, path,
// body, timestamp or nonce.
//
// The function runs as a transport-level wrapper BELOW httpc's retry
// layer — every retry attempt re-invokes fn(req) with the same *http.Request
// (Go re-sets the body via req.GetBody before each attempt), so
// timestamp-bearing signatures stay valid across retries.
//
// If fn must read the request body (e.g. compute a body hash) it should
// drain it via req.GetBody() rather than req.Body — pgx-style — otherwise
// the body is consumed before reaching the upstream.
//
// Panics with *errs.Error on duplicate name or post-Build registration —
// same convention as RegisterRequest/RegisterResponse.
func RegisterAuth(e *Engine, name string, fn func(*http.Request) error) {
	if e.built {
		panic(xerrs.Validationf(CodeAlreadyBuilt,
			"apimap: cannot RegisterAuth %q after Build", name))
	}
	if _, exists := e.authFns[name]; exists {
		panic(xerrs.Validationf(CodeDuplicateCustomAuth,
			"apimap: duplicate RegisterAuth for name %q", name))
	}
	e.authFns[name] = fn
}

func registerType(e *Engine, endpoint string, store map[string]reflect.Type, t reflect.Type, role string) {
	if e.built {
		panic(xerrs.Validationf(CodeAlreadyBuilt,
			"apimap: cannot register %s type for %q after Build", role, endpoint))
	}
	if _, exists := store[endpoint]; exists {
		panic(xerrs.Validationf(CodeDuplicateEndpoint,
			"apimap: duplicate %s registration for endpoint %q", role, endpoint))
	}
	store[endpoint] = t
}

// registrationSet returns the union of req+resp registration keys, used
// by validate() in Build (T8).
func (e *Engine) registrationSet() map[string]struct{} {
	out := map[string]struct{}{}
	for k := range e.reqTypes {
		out[k] = struct{}{}
	}
	for k := range e.respTypes {
		out[k] = struct{}{}
	}
	return out
}

// Build validates the loaded configuration plus registered types and
// returns the runtime *Client. Calling Build twice returns
// CodeAlreadyBuilt. Returned errors are aggregated via errors.Join.
func (e *Engine) Build(opts ...Option) (*Client, error) {
	if e.built {
		return nil, xerrs.Validation(CodeAlreadyBuilt,
			"apimap: Engine.Build called twice")
	}

	cfg := &rawConfig{Clients: e.clients}
	if err := cfg.validate(e.registrationSet()); err != nil {
		return nil, err
	}

	o := &options{}
	for _, fn := range opts {
		fn(o)
	}

	// Cross-check engine-level registrations against the YAML client
	// set so a renamed/missing client surfaces loud rather than as
	// silently-ignored options.
	knownClients := map[string]struct{}{}
	for i := range cfg.Clients {
		knownClients[cfg.Clients[i].Name] = struct{}{}
	}
	var preBuildErrs []error
	for name := range e.clientHTTPCOptions {
		if _, ok := knownClients[name]; !ok {
			preBuildErrs = append(preBuildErrs, xerrs.Validationf(CodeUnknownClient,
				"apimap: RegisterClientOptions references unknown client %q", name))
		}
	}
	for name := range e.clientTransports {
		if _, ok := knownClients[name]; !ok {
			preBuildErrs = append(preBuildErrs, xerrs.Validationf(CodeUnknownClient,
				"apimap: RegisterTransport references unknown client %q", name))
		}
	}
	for name := range e.clientDefaultCalls {
		if _, ok := knownClients[name]; !ok {
			preBuildErrs = append(preBuildErrs, xerrs.Validationf(CodeUnknownClient,
				"apimap: SetClientDefaultCall references unknown client %q", name))
		}
	}
	if err := errors.Join(preBuildErrs...); err != nil {
		return nil, err
	}

	endpoints := map[string]resolvedEndpoint{}
	var buildErrs []error

	for i := range cfg.Clients {
		cl := &cfg.Clients[i]
		// Resolve a custom signer ahead of HTTP-client construction so the
		// transport chain can include it. Header-style auth (basic/bearer/
		// header) is applied later as a per-request header.
		signFn, signErr := e.resolveSigner(cl)
		if signErr != nil {
			buildErrs = append(buildErrs, signErr)
			continue
		}
		// One breaker per client (unit of failure = upstream). Endpoint
		// overrides reuse the same instance so a Stripe outage trips
		// every Stripe endpoint together.
		br, brErr := buildBreaker(cl, o)
		if brErr != nil {
			buildErrs = append(buildErrs, brErr)
			continue
		}
		// Same rule for the bulkhead: one per client, shared across
		// endpoint-override clients. Stripe saturation halts every
		// Stripe endpoint at once.
		bh, bhErr := buildBulkheadFromYAML(cl, o)
		if bhErr != nil {
			buildErrs = append(buildErrs, bhErr)
			continue
		}
		perClientHTTPCOpts := e.clientHTTPCOptions[cl.Name]
		clientTransport := e.clientTransports[cl.Name]
		clientHTTP, err := buildHTTPClient(cl, nil, o, signFn, br, bh, perClientHTTPCOpts, clientTransport)
		if err != nil {
			buildErrs = append(buildErrs, err)
			continue
		}
		authName, authVal := resolveAuthHeader(cl.Auth)
		for j := range cl.Endpoints {
			ep := &cl.Endpoints[j]
			fullName := cl.Name + "." + ep.Name
			pathVars, perr := parsePathTemplate(ep.Path)
			if perr != nil {
				buildErrs = append(buildErrs, perr)
				continue
			}
			epHTTP := clientHTTP
			if ep.hasHTTPCOverride() {
				epHTTP, err = buildHTTPClient(cl, ep, o, signFn, br, bh, perClientHTTPCOpts, clientTransport)
				if err != nil {
					buildErrs = append(buildErrs, err)
					continue
				}
			}
			endpoints[fullName] = resolvedEndpoint{
				clientName:   cl.Name,
				endpointName: ep.Name,
				method:       strings.ToUpper(ep.Method),
				baseURL:      cl.BaseURL,
				pathTemplate: ep.Path,
				pathVars:     pathVars,
				defaultHdrs:  cl.DefaultHeaders,
				authHdrName:  authName,
				authHdrValue: authVal,
				endpointHdrs: ep.Headers,
				encode:       ep.Encode,
				decode:       ep.Decode,
				httpClient:   epHTTP,
				reqType:      e.reqTypes[fullName],
				respType:     e.respTypes[fullName],
			}
		}
	}
	if err := errors.Join(buildErrs...); err != nil {
		return nil, err
	}
	var m *apimapMetrics
	if o.metrics != nil {
		m = newApimapMetrics(o.metrics)
	}
	e.built = true
	return &Client{
		endpoints:          endpoints,
		metrics:            m,
		engineDefaultCall:  o.defaultCall,
		hasEngineDefault:   o.hasDefault,
		clientDefaultCalls: e.clientDefaultCalls,
	}, nil
}

// resolveAuthHeader returns the header name+value to apply for the given
// auth block. Returns ("", "") for nil, type=none, or type=custom auth
// (the custom case is handled transport-side, not as a static header).
// Validation already guaranteed the per-type fields are present.
func resolveAuthHeader(a *rawAuth) (name, value string) {
	if a == nil {
		return "", ""
	}
	switch strings.ToLower(a.Type) {
	case "basic":
		creds := base64.StdEncoding.EncodeToString([]byte(a.Username + ":" + a.Password))
		return "Authorization", "Basic " + creds
	case "bearer":
		return "Authorization", "Bearer " + a.Token
	case "header":
		return a.Name, a.Value
	}
	return "", ""
}

// resolveSigner looks up the request-mutating function for an auth block
// of type=custom against the engine's RegisterAuth registry. Returns
// (nil, nil) for any other type (incl. nil auth). Returns an error of
// CodeUnknownCustomAuth when YAML references a name that was not
// registered.
func (e *Engine) resolveSigner(cl *rawClient) (func(*http.Request) error, error) {
	if cl.Auth == nil || strings.ToLower(cl.Auth.Type) != "custom" {
		return nil, nil
	}
	fn, ok := e.authFns[cl.Auth.Name]
	if !ok {
		return nil, xerrs.Validationf(CodeUnknownCustomAuth,
			"apimap: client %q auth.type=custom references unknown signer %q (RegisterAuth was never called for it)",
			cl.Name, cl.Auth.Name)
	}
	return fn, nil
}

// signingRoundTripper is the transport-level wrapper inserted below
// httpc's retry layer for type=custom auth. RoundTrip mutates req via
// the registered signer, then forwards to base. httpc calls RoundTrip
// once per retry attempt, so every attempt re-signs.
type signingRoundTripper struct {
	sign func(*http.Request) error
	base http.RoundTripper
}

func (s *signingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := s.sign(req); err != nil {
		return nil, err
	}
	return s.base.RoundTrip(req)
}

// buildHTTPClient constructs a *http.Client via httpc.New, applying
// client-level config and (when ep != nil and has overrides) per-endpoint
// overrides. The observability options are passed through unchanged.
// When signFn is non-nil, it is inserted as a transport-level signer
// below httpc's retry layer so every attempt re-invokes the signer.
func buildHTTPClient(cl *rawClient, ep *rawEndpoint, o *options, signFn func(*http.Request) error, br *breaker.Breaker, bh *bulkhead.Bulkhead, perClientOpts []httpc.Option, perClientTransport http.RoundTripper) (*http.Client, error) {
	cfg := httpc.Config{
		Timeout:     cl.Timeout,
		BackoffBase: cl.BackoffBase,
		BackoffMax:  cl.BackoffMax,
	}
	if cl.MaxRetries != nil {
		cfg.MaxRetries = *cl.MaxRetries
	}
	if ep != nil {
		if ep.Timeout != 0 {
			cfg.Timeout = ep.Timeout
		}
		if ep.BackoffBase != 0 {
			cfg.BackoffBase = ep.BackoffBase
		}
		if ep.BackoffMax != 0 {
			cfg.BackoffMax = ep.BackoffMax
		}
		if ep.MaxRetries != nil {
			cfg.MaxRetries = *ep.MaxRetries
		}
	}
	var httpcOpts []httpc.Option
	if o.logger != nil {
		httpcOpts = append(httpcOpts, httpc.WithLogger(o.logger))
	}
	// Intentionally NO httpc.WithMetrics here. apimap owns its own
	// collectors (apimap_*) registered at Build; pushing the same
	// service-wide registry through httpc would re-register httpc_*
	// collectors and panic, since service.New already gave httpc its
	// own WithMetrics. Callers wanting per-upstream httpc_* on a
	// distinct registry can still pass o.metrics → httpc themselves by
	// constructing the *http.Client outside apimap.
	// Layering when signFn is set:
	//   httpc retry → signingRoundTripper → (RegisterTransport | o.baseTransport | http.DefaultTransport)
	// httpc's WithBaseTransport receives the signing wrapper as its base.
	// RegisterTransport (mock) wins over WithBaseTransport when both set —
	// the mock is the most-specific override.
	baseRT := o.baseTransport
	if perClientTransport != nil {
		baseRT = perClientTransport
	}
	if signFn != nil {
		under := baseRT
		if under == nil {
			under = http.DefaultTransport
		}
		baseRT = &signingRoundTripper{sign: signFn, base: under}
	}
	if baseRT != nil {
		httpcOpts = append(httpcOpts, httpc.WithBaseTransport(baseRT))
	}
	if br != nil {
		httpcOpts = append(httpcOpts, httpc.WithBreaker(br))
	}
	if bh != nil {
		httpcOpts = append(httpcOpts, httpc.WithBulkhead(bh))
	}

	// apimap-level hooks — implemented as an httpc middleware so they
	// see the (client, endpoint) pair without needing apimap to wrap
	// every transport manually. The endpoint name is propagated via
	// the request context (see endpointNameFromContext) because a
	// single *http.Client is shared across all endpoints of the same
	// client (only endpoints with httpc overrides get a dedicated
	// instance), so the name cannot be baked into the closure.
	if o.beforeRequest != nil || o.afterResponse != nil {
		clientName := cl.Name
		before := o.beforeRequest
		after := o.afterResponse
		hookMW := func(next http.RoundTripper) http.RoundTripper {
			return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				endpointName := endpointNameFromContext(req.Context())
				if before != nil {
					before(clientName, endpointName, req)
				}
				start := time.Now()
				resp, err := next.RoundTrip(req)
				if after != nil {
					after(clientName, endpointName, req, resp, err, time.Since(start))
				}
				return resp, err
			})
		}
		httpcOpts = append(httpcOpts, httpc.WithMiddleware(hookMW))
	}

	// Engine-wide httpc opts FIRST, then per-client opts so client
	// overrides win. httpc's own middleware/hooks are last-wins, which
	// matches the documented semantics.
	httpcOpts = append(httpcOpts, o.httpcOpts...)
	httpcOpts = append(httpcOpts, perClientOpts...)

	return httpc.New(cfg, httpcOpts...)
}

// roundTripperFunc is the local adaptor for inline middleware
// construction inside buildHTTPClient. Mirrors the stdlib
// http.HandlerFunc pattern.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// buildBulkheadFromYAML materialises the per-client *bulkhead.Bulkhead
// from the optional YAML `bulkhead:` block. Returns (nil, nil) when
// the block is omitted (bulkhead disabled for this client). Logger
// and Metrics inherit from the engine option — collectors disambiguate
// via the `name` const label (= client name).
func buildBulkheadFromYAML(cl *rawClient, o *options) (*bulkhead.Bulkhead, error) {
	if cl.Bulkhead == nil {
		return nil, nil
	}
	rb := cl.Bulkhead
	cfg := bulkhead.Config{
		Name:          cl.Name,
		MaxConcurrent: rb.MaxConcurrent,
		MaxQueue:      rb.MaxQueue,
		QueueTimeout:  rb.QueueTimeout,
		Logger:        o.logger,
		Metrics:       o.metrics,
	}
	b, err := bulkhead.New(cfg)
	if err != nil {
		return nil, xerrs.Wrap(err, xerrs.KindValidation, CodeInvalidBulkhead,
			"apimap: client "+cl.Name+": invalid bulkhead config")
	}
	return b, nil
}

// buildBreaker materialises the per-client *breaker.Breaker from the
// optional YAML `breaker:` block. Returns (nil, nil) when the block
// is omitted (breaker disabled for this client). Logger is inherited
// from the engine option; Metrics is inherited too — breaker_state
// gauges land on the same registry as the apimap collectors and are
// disambiguated by the `name` const label (= client name).
func buildBreaker(cl *rawClient, o *options) (*breaker.Breaker, error) {
	if cl.Breaker == nil {
		return nil, nil
	}
	rb := cl.Breaker
	cfg := breaker.Config{
		Name:              cl.Name,
		FailureThreshold:  rb.FailureThreshold,
		MinimumRequests:   rb.MinimumRequests,
		WindowDuration:    rb.WindowDuration,
		WindowSize:        rb.WindowSize,
		OpenInterval:      rb.OpenInterval,
		HalfOpenMaxProbes: rb.HalfOpenMaxProbes,
		Logger:            o.logger,
		Metrics:           o.metrics,
	}
	b, err := breaker.New(cfg)
	if err != nil {
		return nil, xerrs.Wrap(err, xerrs.KindValidation, CodeInvalidBreaker,
			"apimap: client "+cl.Name+": invalid breaker config")
	}
	return b, nil
}
