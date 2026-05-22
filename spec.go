package fibermap

// rawConfig is the top-level YAML document.
type rawConfig struct {
	MiddlewareSets map[string][]string `yaml:"middleware_sets"`
	Groups         []rawGroup          `yaml:"groups"`
}

// rawGroup is one entry under `groups:`. Groups can nest via `Groups`.
type rawGroup struct {
	Prefix        string     `yaml:"prefix"`
	Middleware    []string   `yaml:"middleware"`
	MiddlewareSet string     `yaml:"middleware_set"`
	Routes        []rawRoute `yaml:"routes"`
	Groups        []rawGroup `yaml:"groups"`
}

// rawRoute is one entry under `routes:`.
type rawRoute struct {
	Method        string   `yaml:"method"`
	Path          string   `yaml:"path"`
	Handler       string   `yaml:"handler"`
	Middleware    []string `yaml:"middleware"`
	MiddlewareSet string   `yaml:"middleware_set"`
	Roles         []string `yaml:"roles"`
	Name          string   `yaml:"name"`
	Tags          []string `yaml:"tags"`
	Description   string   `yaml:"description"`
}

// RouteInfo is the public introspection record returned by Engine.Routes().
type RouteInfo struct {
	Method      string
	Path        string
	Handler     string
	Name        string
	Description string
	Middleware  []string
	Roles       []string
	Tags        []string
}
