package fibermap

// rawConfig is the top-level YAML document.
type rawConfig struct {
	MiddlewareSets map[string][]mwRef `yaml:"middleware_sets"`
	Groups         []rawGroup         `yaml:"groups"`
}

// rawGroup is one entry under `groups:`. Groups can nest via `Groups`.
type rawGroup struct {
	Prefix        string     `yaml:"prefix"`
	Middleware    []mwRef    `yaml:"middleware"`
	MiddlewareSet string     `yaml:"middleware_set"`
	Routes        []rawRoute `yaml:"routes"`
	Groups        []rawGroup `yaml:"groups"`
}

// rawRoute is one entry under `routes:`.
type rawRoute struct {
	Method        string   `yaml:"method"`
	Path          string   `yaml:"path"`
	Handler       string   `yaml:"handler"`
	Middleware    []mwRef  `yaml:"middleware"`
	MiddlewareSet string   `yaml:"middleware_set"`
	Name          string   `yaml:"name"`
	Tags          []string `yaml:"tags"`
	Description   string   `yaml:"description"`
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
type MiddlewareRef struct {
	Name string
	Args []string
}

// RouteInfo is the public introspection record returned by Engine.Routes().
type RouteInfo struct {
	Method      string
	Path        string
	Handler     string
	Name        string
	Description string
	Middleware  []MiddlewareRef
	Tags        []string
}
