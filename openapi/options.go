package openapi

// Option configures a [Generator]. Options are applied in order
// during [NewGenerator]; later writes win for fields a single option
// sets, but accumulating options (Servers, Security schemes,
// MapMiddlewareToSecurity) append.
type Option func(*config)

// config holds the non-generic state a Generator carries between
// NewGenerator and Generate. Options write into it.
type config struct {
	info               Info
	servers            []Server
	securitySchemes    map[string]SecurityScheme
	middlewareSecurity map[string]string // middleware name → security scheme name
}

func newConfig() *config {
	return &config{
		info:               Info{Title: "fibermap API", Version: "0.0.0"},
		securitySchemes:    map[string]SecurityScheme{},
		middlewareSecurity: map[string]string{},
	}
}

// WithInfo overrides the document `info` block (title, version,
// description, contact). The default is
// `{Title: "fibermap API", Version: "0.0.0"}`.
func WithInfo(info Info) Option {
	return func(c *config) { c.info = info }
}

// WithServer appends a `server` entry (the OpenAPI document can
// declare multiple — prod / staging / etc).
func WithServer(url, description string) Option {
	return func(c *config) {
		c.servers = append(c.servers, Server{URL: url, Description: description})
	}
}

// WithSecurity registers a security scheme under `name`. Use
// [HTTPBearer], [HTTPBasic], or [APIKey] to construct the scheme,
// or fill in your own.
func WithSecurity(name string, scheme SecurityScheme) Option {
	return func(c *config) { c.securitySchemes[name] = scheme }
}

// MapMiddlewareToSecurity attaches a security scheme name to a
// fibermap middleware name. Routes whose resolved middleware chain
// includes `middleware` get `security: [{schemeName: []}]` on their
// generated operation.
//
// The scheme must also be registered via [WithSecurity]; otherwise
// Generate fails with an error pointing at the missing reference.
//
//	openapi.WithSecurity("BearerAuth", openapi.HTTPBearer("JWT"))
//	openapi.MapMiddlewareToSecurity("auth", "BearerAuth")
func MapMiddlewareToSecurity(middleware, schemeName string) Option {
	return func(c *config) { c.middlewareSecurity[middleware] = schemeName }
}
