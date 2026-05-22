package fibermap

// rawConfig is the top-level YAML document.
type rawConfig struct {
	MiddlewareSets map[string][]mwRef `yaml:"middleware_sets"`
	Groups         []rawGroup         `yaml:"groups"`
}

// rawGroup is one entry under `groups:`. Groups can nest via `Groups`.
// Line is the 1-based source-file line where this entry starts; populated
// by UnmarshalYAML and used to annotate parse/mount errors.
type rawGroup struct {
	Prefix        string     `yaml:"prefix"`
	Middleware    []mwRef    `yaml:"middleware"`
	MiddlewareSet string     `yaml:"middleware_set"`
	Routes        []rawRoute `yaml:"routes"`
	Groups        []rawGroup `yaml:"groups"`
	Line          int        `yaml:"-"`
}

// rawRoute is one entry under `routes:`. Line: see rawGroup.
type rawRoute struct {
	Method        string   `yaml:"method"`
	Path          string   `yaml:"path"`
	Handler       string   `yaml:"handler"`
	Middleware    []mwRef  `yaml:"middleware"`
	MiddlewareSet string   `yaml:"middleware_set"`
	Name          string   `yaml:"name"`
	Tags          []string `yaml:"tags"`
	Description   string   `yaml:"description"`
	Line          int      `yaml:"-"`
}

// mwRef is a reference to a middleware in YAML. It is either a scalar string
// (plain middleware registered via RegisterMiddleware) or a single-key map
// {name: [args...]} (parameterized middleware registered via
// RegisterMiddlewareFactory). Args is nil for the scalar form.
type mwRef struct {
	Name string
	Args []string
}

// MiddlewareRef is the public form of mwRef surfaced via RouteInfo.
// Args is nil for plain (scalar) middleware, non-nil for factory calls
// (even if the factory was invoked with zero args).
type MiddlewareRef struct {
	Name string   `json:"name"`
	Args []string `json:"args,omitempty"`
}

// RouteInfo is the public introspection record returned by Engine.Routes().
// JSON tags are provided so users can expose Routes() over an admin
// endpoint or dump it for tooling without an extra wrapper struct.
type RouteInfo struct {
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	Handler     string          `json:"handler"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Middleware  []MiddlewareRef `json:"middleware,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
}
