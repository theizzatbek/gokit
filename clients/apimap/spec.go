package apimap

import "time"

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
