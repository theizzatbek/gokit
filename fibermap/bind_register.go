package fibermap

import "github.com/theizzatbek/gokit/fibermap/bind"

// RegisterHandler is the package-level form of
// [Engine.RegisterHandler]. It exists so callers can use a uniform
// `fibermap.Register*` style across plain handlers, body-binding
// handlers, middleware, and factories (the body-binding helpers
// have to be package-level functions because Go does not allow
// generic methods, so package-level is the lowest common surface).
//
// Behaviour and panic semantics are identical to the method form.
//
//	fibermap.RegisterHandler(eng, "tasks.list", taskH.List,
//	    fibermap.WithResponse(200, ListResp{}),
//	)
func RegisterHandler[T any](e *Engine[T], name string, h HandlerFunc[T], opts ...HandlerOption) {
	e.RegisterHandler(name, h, opts...)
}

// RegisterMiddleware is the package-level form of
// [Engine.RegisterMiddleware].
func RegisterMiddleware[T any](e *Engine[T], name string, m MiddlewareFunc[T]) {
	e.RegisterMiddleware(name, m)
}

// RegisterMiddlewareFactory is the package-level form of
// [Engine.RegisterMiddlewareFactory].
func RegisterMiddlewareFactory[T any](e *Engine[T], name string, f MiddlewareFactoryFunc[T]) {
	e.RegisterMiddlewareFactory(name, f)
}

// RegisterHandlerWithBody registers a typed-body handler. The
// wrapper parses the request body into Req via bind.Body (using the
// engine's validator if set), then invokes h with the populated
// value.
//
// One source of truth for the request type: it appears in h's
// signature and is automatically attached as a schema for OpenAPI
// generation. No separate bind.Body call inside the handler, no
// extra WithBody option.
//
//	type CreateTaskReq struct {
//	    Title string `json:"title" validate:"required,min=1,max=200"`
//	}
//
//	func (h *Handler) Create(c *Ctx, req CreateTaskReq) error {
//	    // req is already parsed + validated.
//	    return c.Status(201).JSON(...)
//	}
//
//	fibermap.RegisterHandlerWithBody(eng, "tasks.create", h.Create,
//	    fibermap.WithResponse(201, Task{}),
//	)
//
// Parse / validate failures are routed through the engine's
// BindErrorFunc (default 400 JSON; customise via
// [Engine.SetBindErrorHandler]).
//
// Extra HandlerOption values (WithResponse, WithQuery, WithHeaders,
// …) are forwarded to RegisterHandler — only the request-body
// schema is auto-derived from Req.
func RegisterHandlerWithBody[T, Req any](e *Engine[T], name string, h func(*Context[T], Req) error, opts ...HandlerOption) {
	wrapped := func(c *Context[T]) error {
		req, err := bind.Body[Req](c.Ctx, e.validator)
		if err != nil {
			return e.bindError(c, err)
		}
		return h(c, req)
	}
	all := append([]HandlerOption{WithBody(zero[Req]())}, opts...)
	e.RegisterHandler(name, wrapped, all...)
}

// RegisterHandlerWithQuery is the query-string analogue of
// [RegisterHandlerWithBody]. Fields on Req use the `query:` tag.
func RegisterHandlerWithQuery[T, Req any](e *Engine[T], name string, h func(*Context[T], Req) error, opts ...HandlerOption) {
	wrapped := func(c *Context[T]) error {
		req, err := bind.Query[Req](c.Ctx, e.validator)
		if err != nil {
			return e.bindError(c, err)
		}
		return h(c, req)
	}
	all := append([]HandlerOption{WithQuery(zero[Req]())}, opts...)
	e.RegisterHandler(name, wrapped, all...)
}

// RegisterHandlerWithParams is the route-param analogue of
// [RegisterHandlerWithBody]. Fields on Req use the `params:` tag.
// The Req schema is auto-attached via WithParams so OpenAPI
// generation picks up its validate-derived constraints.
func RegisterHandlerWithParams[T, Req any](e *Engine[T], name string, h func(*Context[T], Req) error, opts ...HandlerOption) {
	wrapped := func(c *Context[T]) error {
		req, err := bind.Params[Req](c.Ctx, e.validator)
		if err != nil {
			return e.bindError(c, err)
		}
		return h(c, req)
	}
	all := append([]HandlerOption{WithParams(zero[Req]())}, opts...)
	e.RegisterHandler(name, wrapped, all...)
}

// RegisterHandlerWithHeaders is the header analogue of
// [RegisterHandlerWithBody]. Fields on Req use the `reqHeader:` tag.
func RegisterHandlerWithHeaders[T, Req any](e *Engine[T], name string, h func(*Context[T], Req) error, opts ...HandlerOption) {
	wrapped := func(c *Context[T]) error {
		req, err := bind.Header[Req](c.Ctx, e.validator)
		if err != nil {
			return e.bindError(c, err)
		}
		return h(c, req)
	}
	all := append([]HandlerOption{WithHeaders(zero[Req]())}, opts...)
	e.RegisterHandler(name, wrapped, all...)
}

// zero returns the zero value of T. Equivalent to `*new(T)` but
// reads more clearly at call sites.
func zero[T any]() T {
	var z T
	return z
}
