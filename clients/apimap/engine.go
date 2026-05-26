package apimap

import (
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"

	"github.com/theizzatbek/gokit/clients/httpc"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Engine is the build-once configurator. New → LoadFile/LoadBytes (n) →
// RegisterRequest/RegisterResponse (n) → Build (once).
type Engine struct {
	clients []rawClient

	reqTypes  map[string]reflect.Type // endpoint full-name → registered request type
	respTypes map[string]reflect.Type // endpoint full-name → registered response type

	envMap map[string]string // nil = no overrides; LoadBytes builds composite lookup

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
		reqTypes:  map[string]reflect.Type{},
		respTypes: map[string]reflect.Type{},
	}
	for _, fn := range opts {
		fn(e)
	}
	return e
}

// LoadFile reads a YAML file and appends its clients to the engine.
// May be called multiple times (one file per upstream API is the
// expected pattern).
func (e *Engine) LoadFile(path string) error {
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

// RegisterRequest records the Go type used by Exchange for the given
// endpoint. Panics with *errs.Error if called after Build (matches
// fibermap's RegisterHandler convention — duplicate or post-Build
// registration is a programmer error at startup).
func RegisterRequest[T any](e *Engine, endpoint string) {
	registerType(e, endpoint, e.reqTypes, reflect.TypeOf((*T)(nil)).Elem(), "request")
}

// RegisterResponse records the Go type used by Decode/Exchange for the
// given endpoint. Same panic semantics as RegisterRequest.
func RegisterResponse[T any](e *Engine, endpoint string) {
	registerType(e, endpoint, e.respTypes, reflect.TypeOf((*T)(nil)).Elem(), "response")
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

	endpoints := map[string]resolvedEndpoint{}
	var buildErrs []error

	for i := range cfg.Clients {
		cl := &cfg.Clients[i]
		clientHTTP, err := buildHTTPClient(cl, nil, o)
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
				epHTTP, err = buildHTTPClient(cl, ep, o)
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
	e.built = true
	return &Client{endpoints: endpoints}, nil
}

// resolveAuthHeader returns the header name+value to apply for the given
// auth block. Returns ("", "") for nil or type=none auth. Validation
// already guaranteed the per-type fields are present; this function
// just assembles the header.
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

// buildHTTPClient constructs a *http.Client via httpc.New, applying
// client-level config and (when ep != nil and has overrides) per-endpoint
// overrides. The observability options are passed through unchanged.
func buildHTTPClient(cl *rawClient, ep *rawEndpoint, o *options) (*http.Client, error) {
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
	if o.metrics != nil {
		httpcOpts = append(httpcOpts, httpc.WithMetrics(o.metrics))
	}
	if o.baseTransport != nil {
		httpcOpts = append(httpcOpts, httpc.WithBaseTransport(o.baseTransport))
	}
	return httpc.New(cfg, httpcOpts...)
}
