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
	middlewareSecurity map[string][]string // middleware name → list of security scheme names (OR semantics)
	defaultResponses   map[int]any         // applied to every operation; per-route schemas override per-status
}

func newConfig() *config {
	return &config{
		info:               Info{Title: "fibermap API", Version: "0.0.0"},
		securitySchemes:    map[string]SecurityScheme{},
		middlewareSecurity: map[string][]string{},
		defaultResponses:   map[int]any{},
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
// Multiple calls for the same middleware accumulate — both schemes
// are listed in the operation's `security` array (OR semantics: any
// one satisfies the requirement). This lets an `auth` middleware
// that accepts both Bearer and Basic credentials advertise both in
// the spec.
//
// The scheme must also be registered via [WithSecurity]; otherwise
// Generate fails with an error pointing at the missing reference.
// Prefer [SecurityMapping] for the common "scheme + one or more
// middleware names" case.
//
//	openapi.WithSecurity("BearerAuth", openapi.HTTPBearer("JWT"))
//	openapi.MapMiddlewareToSecurity("auth", "BearerAuth")
func MapMiddlewareToSecurity(middleware, schemeName string) Option {
	return func(c *config) {
		c.middlewareSecurity[middleware] = append(c.middlewareSecurity[middleware], schemeName)
	}
}

// WithDefaultResponse adds a response schema applied to every
// operation in the generated spec, unless the operation declared
// its own schema for that status code (via [fibermap.WithResponse]
// at RegisterHandler time, which wins).
//
// Typical use is to declare the boring universal errors — 400 / 401
// / 403 / 404 / 500 — once instead of repeating them on every
// handler:
//
//	type ErrorResponse struct {
//	    Error string `json:"error"`
//	}
//
//	gen := openapi.NewGenerator(eng,
//	    openapi.WithDefaultResponse(400, ErrorResponse{}),
//	    openapi.WithDefaultResponse(401, ErrorResponse{}),
//	    openapi.WithDefaultResponse(403, ErrorResponse{}),
//	    openapi.WithDefaultResponse(404, ErrorResponse{}),
//	    openapi.WithDefaultResponse(500, ErrorResponse{}),
//	)
//
// Routes still declare their HAPPY-path responses (`201 Task{}`,
// `200 ListResp{}`, …) and any non-universal statuses (`422`) via
// [fibermap.WithResponse]. Pass nil model for an empty-body default
// (e.g. `WithDefaultResponse(204, nil)`).
//
// Multiple calls accumulate; later calls for the same status code
// overwrite earlier ones.
func WithDefaultResponse(status int, model any) Option {
	return func(c *config) {
		c.defaultResponses[status] = model
	}
}

// SecurityMapping registers a security scheme AND attaches one or
// more middleware names to it in a single call. Equivalent to
// [WithSecurity] plus one [MapMiddlewareToSecurity] per name:
//
//	openapi.SecurityMapping("BearerAuth", openapi.HTTPBearer("JWT"), "auth")
//
// Mapping the same middleware to multiple schemes is supported and
// produces OR semantics on the operation (any scheme satisfies):
//
//	openapi.SecurityMapping("BearerAuth", openapi.HTTPBearer(), "auth"),
//	openapi.SecurityMapping("BasicAuth",  openapi.HTTPBasic(),  "auth"),
//
// Pass multiple middleware names if more than one fibermap middleware
// maps to the same scheme (e.g. `auth` and `auth_optional` both
// satisfy `BearerAuth`).
func SecurityMapping(schemeName string, scheme SecurityScheme, middlewares ...string) Option {
	return func(c *config) {
		c.securitySchemes[schemeName] = scheme
		for _, mw := range middlewares {
			c.middlewareSecurity[mw] = append(c.middlewareSecurity[mw], schemeName)
		}
	}
}
