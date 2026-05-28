package fibermap

// HandlerOption attaches typed request/response metadata to a
// handler at registration time. The metadata is stored as `any` —
// fibermap core does not interpret it. Consumers like
// fibermap/openapi reflect on the values to generate JSON-Schema
// documentation.
//
//	eng.RegisterHandler("tasks.create", taskH.Create,
//	    fibermap.WithBody(CreateReq{}),
//	    fibermap.WithResponse(201, Task{}),
//	    fibermap.WithResponse(400, ErrorResponse{}),
//	)
//
// All options are optional — handlers registered without any are
// still valid; they just lack documented schemas.
type HandlerOption func(*HandlerMeta)

// HandlerMeta holds typed schemas attached to a handler via
// [HandlerOption] values passed to [Engine.RegisterHandler]. The
// fields are opaque `any` — pass the zero value of a Go struct
// (e.g. `CreateReq{}`); consumers reflect on the runtime type.
//
// Returned by [Engine.HandlerMeta]. Callers must treat the returned
// pointer's contents as read-only — fibermap does not deep-copy.
type HandlerMeta struct {
	// Body is the request body model — typically a struct with
	// `json:` and `validate:` tags. nil when no body was declared.
	Body any
	// Query is the query-string model — fields use the `query:` tag.
	Query any
	// Params is the route-parameter model — fields use the `params:`
	// tag. When set, OpenAPI generation uses this struct's schema for
	// path parameters instead of synthesizing plain string entries
	// from the URL pattern (picks up validate-derived constraints,
	// descriptions, etc).
	Params any
	// Headers is the request-header model — fields use the
	// `reqHeader:` tag.
	Headers any
	// Responses maps HTTP status code to the response body model.
	// nil model on a status means "empty body" (use for 204).
	Responses map[int]any
}

// WithBody attaches a request-body schema model to the handler.
// Pass the zero value of your request struct:
//
//	eng.RegisterHandler("tasks.create", h.Create, fibermap.WithBody(CreateReq{}))
func WithBody(model any) HandlerOption {
	return func(m *HandlerMeta) { m.Body = model }
}

// WithQuery attaches a query-string schema model.
func WithQuery(model any) HandlerOption {
	return func(m *HandlerMeta) { m.Query = model }
}

// WithParams attaches a route-parameter schema model — typically a
// struct with `params:` and `validate:` tags. OpenAPI generation
// reads the model's fields to enrich each path parameter with the
// declared schema (descriptions, validate-derived constraints,
// custom types). When omitted, path parameters fall back to plain
// `string` entries derived from the URL pattern.
func WithParams(model any) HandlerOption {
	return func(m *HandlerMeta) { m.Params = model }
}

// WithHeaders attaches a request-header schema model.
func WithHeaders(model any) HandlerOption {
	return func(m *HandlerMeta) { m.Headers = model }
}

// WithResponse declares the schema for one HTTP response status.
// Multiple calls accumulate; passing nil for `model` advertises an
// empty body (typical for 204 No Content).
//
//	fibermap.WithResponse(201, Task{}),
//	fibermap.WithResponse(400, ErrorResponse{}),
//	fibermap.WithResponse(204, nil),
func WithResponse(status int, model any) HandlerOption {
	return func(m *HandlerMeta) {
		if m.Responses == nil {
			m.Responses = map[int]any{}
		}
		m.Responses[status] = model
	}
}

// applyHandlerOptions runs each option against a fresh HandlerMeta
// and returns it. Returns nil when opts is empty so the engine can
// store nothing.
func applyHandlerOptions(opts []HandlerOption) *HandlerMeta {
	if len(opts) == 0 {
		return nil
	}
	m := &HandlerMeta{}
	for _, opt := range opts {
		opt(m)
	}
	return m
}
