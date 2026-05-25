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
	Summary       string   `yaml:"summary"`
	Description   string   `yaml:"description"`
	// Timeout is a Go duration string ("5s", "300ms"). Empty means no
	// per-route timeout. Parsed at LoadFile/LoadBytes time;
	// CodeInvalidTimeout on malformed value.
	Timeout string `yaml:"timeout"`
	// Cache enables response caching for the route. nil → no cache.
	// Two accepted YAML shapes (see rawCache.UnmarshalYAML):
	//   cache: 30s
	//   cache: {ttl: 30s, control: true, headers: true,
	//           vary_header: [Accept-Language]}
	Cache *rawCache `yaml:"cache"`
	Line  int       `yaml:"-"`
}

// rawCache is the route-level cache configuration. Engine-wide knobs
// (Storage, KeyBy, MaxBytes) live on Engine via SetCacheDefaults.
type rawCache struct {
	// TTL is a Go duration string ("30s", "5m"). Required, > 0.
	TTL string `yaml:"ttl"`
	// Control: respect `Cache-Control: no-store` / `no-cache` request
	// headers (forwarded to fiber.cache.Config.CacheControl).
	Control bool `yaml:"control"`
	// Headers: also cache and replay handler-set response headers
	// (forwarded to fiber.cache.Config.StoreResponseHeaders).
	Headers bool `yaml:"headers"`
	// VaryHeader: request headers whose values get folded into the
	// cache key, so two requests with different Accept-Language values
	// keep separate cache entries.
	VaryHeader []string `yaml:"vary_header"`
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
//
// Timeout is the verbatim YAML duration string ("5s") — empty when no
// per-route timeout was declared. Kept as a string so JSON
// admin-endpoint output stays human-readable.
//
// Source identifies where the route came from: "yaml" for routes
// declared in routes.yaml, "programmatic" for routes added via
// Engine.Add. Useful for ops tools that want to distinguish
// declarative from imperative routes.
type RouteInfo struct {
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	Handler     string          `json:"handler"`
	Name        string          `json:"name,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	Description string          `json:"description,omitempty"`
	Middleware  []MiddlewareRef `json:"middleware,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
	Timeout     string          `json:"timeout,omitempty"`
	Cache       *CacheInfo      `json:"cache,omitempty"`
	Source      string          `json:"source,omitempty"`
}

// Route source constants used in RouteInfo.Source.
const (
	SourceYAML         = "yaml"
	SourceProgrammatic = "programmatic"
)

// CacheInfo is the public introspection form of a route's cache
// configuration. nil → no cache.
type CacheInfo struct {
	TTL        string   `json:"ttl"`
	Control    bool     `json:"control,omitempty"`
	Headers    bool     `json:"headers,omitempty"`
	VaryHeader []string `json:"vary_header,omitempty"`
}
