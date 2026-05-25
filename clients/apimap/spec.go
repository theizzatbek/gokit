package apimap

import (
	"errors"
	"net/url"
	"strings"
	"time"

	xerrs "github.com/theizzatbek/fibermap/errs"
)

// rawConfig mirrors the top-level YAML document. Multi-file loads
// (LoadFile called multiple times) append rawConfig.Clients into the
// engine's flat list, validated together at Build.
type rawConfig struct {
	Clients []rawClient `yaml:"clients"`
}

// rawClient is one upstream API description.
type rawClient struct {
	Name           string            `yaml:"name"`
	BaseURL        string            `yaml:"base_url"`
	Timeout        time.Duration     `yaml:"timeout,omitempty"`
	MaxRetries     *int              `yaml:"max_retries,omitempty"`
	BackoffBase    time.Duration     `yaml:"backoff_base,omitempty"`
	BackoffMax     time.Duration     `yaml:"backoff_max,omitempty"`
	DefaultHeaders map[string]string `yaml:"default_headers,omitempty"`
	Auth           *rawAuth          `yaml:"auth,omitempty"`
	Endpoints      []rawEndpoint     `yaml:"endpoints"`
}

// rawAuth carries the YAML auth: block. type discriminator picks one
// of {basic, bearer, header, none}; per-type fields are checked at
// validate() time.
type rawAuth struct {
	Type     string `yaml:"type"`
	Username string `yaml:"username,omitempty"` // basic
	Password string `yaml:"password,omitempty"` // basic
	Token    string `yaml:"token,omitempty"`    // bearer
	Name     string `yaml:"name,omitempty"`     // header
	Value    string `yaml:"value,omitempty"`    // header
}

// rawEndpoint is one routed operation on an upstream API.
type rawEndpoint struct {
	Name        string            `yaml:"name"`
	Method      string            `yaml:"method"`
	Path        string            `yaml:"path"`
	Encode      string            `yaml:"encode,omitempty"`
	Decode      string            `yaml:"decode,omitempty"`
	Headers     map[string]string `yaml:"headers,omitempty"`
	Timeout     time.Duration     `yaml:"timeout,omitempty"`
	MaxRetries  *int              `yaml:"max_retries,omitempty"`
	BackoffBase time.Duration     `yaml:"backoff_base,omitempty"`
	BackoffMax  time.Duration     `yaml:"backoff_max,omitempty"`
}

// hasHTTPCOverride reports whether the endpoint redefines any field that
// would force a dedicated *http.Client at Build time.
func (e rawEndpoint) hasHTTPCOverride() bool {
	return e.Timeout != 0 ||
		e.MaxRetries != nil ||
		e.BackoffBase != 0 ||
		e.BackoffMax != 0
}

// validHTTPMethods is the set of methods accepted in an endpoint's
// `method:` field (case-insensitive at decode time).
var validHTTPMethods = map[string]struct{}{
	"GET": {}, "HEAD": {}, "POST": {}, "PUT": {},
	"PATCH": {}, "DELETE": {}, "OPTIONS": {},
}

var validEncodings = map[string]struct{}{
	"":     {},
	"none": {}, "json": {}, "form": {}, "raw": {},
}

var validDecodings = map[string]struct{}{
	"":     {},
	"none": {}, "json": {}, "raw": {},
}

var validAuthTypes = map[string]struct{}{
	"":     {},
	"none": {}, "basic": {}, "bearer": {}, "header": {},
}

// validate checks the auth block per its type discriminator. Returns nil
// for omitted (a == nil) or type=none.
func (a *rawAuth) validate(clientName string) error {
	if a == nil {
		return nil
	}
	t := strings.ToLower(a.Type)
	if _, ok := validAuthTypes[t]; !ok {
		return xerrs.Validationf(CodeAuthInvalidType,
			"apimap: client %q auth.type %q not in {basic, bearer, header, none}",
			clientName, a.Type)
	}
	switch t {
	case "", "none":
		return nil
	case "basic":
		if a.Username == "" || a.Password == "" {
			return xerrs.Validationf(CodeAuthMissingField,
				"apimap: client %q auth.type=basic requires username and password", clientName)
		}
	case "bearer":
		if a.Token == "" {
			return xerrs.Validationf(CodeAuthMissingField,
				"apimap: client %q auth.type=bearer requires token", clientName)
		}
	case "header":
		if a.Name == "" || a.Value == "" {
			return xerrs.Validationf(CodeAuthMissingField,
				"apimap: client %q auth.type=header requires name and value", clientName)
		}
	}
	return nil
}

// validate aggregates all validation failures and returns them via errors.Join.
// registrations is the set of "client.endpoint" names registered with
// RegisterRequest/RegisterResponse; pass nil if there are none.
func (c *rawConfig) validate(registrations map[string]struct{}) error {
	var errsAcc []error
	seenClients := map[string]struct{}{}

	for i := range c.Clients {
		cl := &c.Clients[i]
		if cl.Name == "" {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeMissingClientName,
				"apimap: clients[%d] missing name", i))
		} else {
			if _, dup := seenClients[cl.Name]; dup {
				errsAcc = append(errsAcc, xerrs.Validationf(CodeDuplicateClient,
					"apimap: duplicate client name %q", cl.Name))
			}
			seenClients[cl.Name] = struct{}{}
		}
		if u, err := url.Parse(cl.BaseURL); err != nil || u.Scheme == "" || u.Host == "" {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidBaseURL,
				"apimap: client %q has invalid base_url %q", cl.Name, cl.BaseURL))
		}
		if aerr := cl.Auth.validate(cl.Name); aerr != nil {
			errsAcc = append(errsAcc, aerr)
		}
		seenEndpoints := map[string]struct{}{}
		for j := range cl.Endpoints {
			ep := &cl.Endpoints[j]
			if _, dup := seenEndpoints[ep.Name]; dup {
				errsAcc = append(errsAcc, xerrs.Validationf(CodeDuplicateEndpoint,
					"apimap: client %q has duplicate endpoint %q", cl.Name, ep.Name))
			}
			seenEndpoints[ep.Name] = struct{}{}

			method := strings.ToUpper(ep.Method)
			if _, ok := validHTTPMethods[method]; !ok {
				errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidMethod,
					"apimap: client %q endpoint %q method %q is not a valid HTTP method",
					cl.Name, ep.Name, ep.Method))
			}

			if _, perr := parsePathTemplate(ep.Path); perr != nil {
				errsAcc = append(errsAcc, perr)
			}

			if _, ok := validEncodings[ep.Encode]; !ok {
				errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidEncode,
					"apimap: client %q endpoint %q invalid encode %q (allowed: none|json|form|raw)",
					cl.Name, ep.Name, ep.Encode))
			}

			if _, ok := validDecodings[ep.Decode]; !ok {
				errsAcc = append(errsAcc, xerrs.Validationf(CodeInvalidDecode,
					"apimap: client %q endpoint %q invalid decode %q (allowed: none|json|raw)",
					cl.Name, ep.Name, ep.Decode))
			}
		}
	}

	have := c.endpointSet()
	for full := range registrations {
		if _, ok := have[full]; !ok {
			errsAcc = append(errsAcc, xerrs.Validationf(CodeRegisteredEndpointMissing,
				"apimap: registered type for unknown endpoint %q", full))
		}
	}
	return errors.Join(errsAcc...)
}

// endpointSet returns the set of "client.endpoint" names declared in the
// config. Used for cross-checking type registrations at Build.
func (c *rawConfig) endpointSet() map[string]struct{} {
	out := map[string]struct{}{}
	for i := range c.Clients {
		cl := &c.Clients[i]
		for j := range cl.Endpoints {
			out[cl.Name+"."+cl.Endpoints[j].Name] = struct{}{}
		}
	}
	return out
}
