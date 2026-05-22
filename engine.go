package fibermap

import (
	"errors"
	"fmt"

	"github.com/gofiber/fiber/v2"
)

type (
	HandlerFunc[T any]           func(c *Context[T]) error
	MiddlewareFunc[T any]        func(c *Context[T]) error
	MiddlewareFactoryFunc[T any] func(args []string) (MiddlewareFunc[T], error)
	ContextBuilder[T any]        func(c *fiber.Ctx) (T, error)
	ContextErrorFunc             func(c *fiber.Ctx, err error) error
)

// Engine is the build-once-mount-once configurator. It is parameterized by T,
// the per-request payload type produced by ContextBuilder.
type Engine[T any] struct {
	builder     ContextBuilder[T]
	handlers    map[string]HandlerFunc[T]
	middlewares map[string]MiddlewareFunc[T]
	factories   map[string]MiddlewareFactoryFunc[T]
	ctxError    ContextErrorFunc

	cfg     *rawConfig
	cfgFile string

	routes  []RouteInfo
	mounted bool
}

// New constructs an empty Engine. Call SetContextBuilder, RegisterHandler,
// RegisterMiddleware (and optionally RegisterMiddlewareFactory) before
// LoadFile/LoadBytes and Mount.
func New[T any]() *Engine[T] {
	return &Engine[T]{
		handlers:    map[string]HandlerFunc[T]{},
		middlewares: map[string]MiddlewareFunc[T]{},
		factories:   map[string]MiddlewareFactoryFunc[T]{},
		ctxError:    defaultContextError,
	}
}

// SetContextBuilder installs the function that builds the per-request Data.
// Calling more than once silently overwrites.
func (e *Engine[T]) SetContextBuilder(fn ContextBuilder[T]) { e.builder = fn }

// SetContextErrorHandler overrides the default response when ContextBuilder
// returns an error.
func (e *Engine[T]) SetContextErrorHandler(h ContextErrorFunc) { e.ctxError = h }

// RegisterHandler registers a handler under a name referenced from YAML.
// Returns *Error with CodeDuplicateRegistration if the name is already taken.
func (e *Engine[T]) RegisterHandler(name string, h HandlerFunc[T]) error {
	if _, ok := e.handlers[name]; ok {
		return &Error{Stage: "register", Code: CodeDuplicateRegistration, Message: "handler " + name + " already registered"}
	}
	e.handlers[name] = h
	return nil
}

// RegisterMiddleware registers a plain (no-args) middleware. YAML references
// it as a scalar string. Returns *Error with CodeDuplicateRegistration if
// the name is already taken (in either the plain or factory registry).
func (e *Engine[T]) RegisterMiddleware(name string, m MiddlewareFunc[T]) error {
	if _, ok := e.middlewares[name]; ok {
		return &Error{Stage: "register", Code: CodeDuplicateRegistration, Message: "middleware " + name + " already registered"}
	}
	if _, ok := e.factories[name]; ok {
		return &Error{Stage: "register", Code: CodeDuplicateRegistration, Message: "name " + name + " already registered as a middleware factory"}
	}
	e.middlewares[name] = m
	return nil
}

// RegisterMiddlewareFactory registers a parameterized middleware. YAML
// references it as a single-key mapping {name: [args...]}. The factory is
// invoked once per (name, args) pair at Mount time; the returned
// MiddlewareFunc is cached for the lifetime of the engine.
func (e *Engine[T]) RegisterMiddlewareFactory(name string, f MiddlewareFactoryFunc[T]) error {
	if _, ok := e.factories[name]; ok {
		return &Error{Stage: "register", Code: CodeDuplicateRegistration, Message: "middleware factory " + name + " already registered"}
	}
	if _, ok := e.middlewares[name]; ok {
		return &Error{Stage: "register", Code: CodeDuplicateRegistration, Message: "name " + name + " already registered as a plain middleware"}
	}
	e.factories[name] = f
	return nil
}

// LoadFile reads and parses a YAML file.
func (e *Engine[T]) LoadFile(path string) error {
	cfg, err := loadFileToConfig(path)
	if err != nil {
		return err
	}
	e.cfg = cfg
	e.cfgFile = path
	return nil
}

// LoadBytes parses YAML from memory.
func (e *Engine[T]) LoadBytes(data []byte) error {
	cfg, err := parseBytes(data, "")
	if err != nil {
		return err
	}
	e.cfg = cfg
	e.cfgFile = ""
	return nil
}

func defaultContextError(c *fiber.Ctx, err error) error {
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "context build failed"})
}

// Mount validates the loaded YAML against registered handlers/middleware and
// installs routes on `router`. If validation produces any errors they are all
// returned (errors.Join) and no routes are installed.
//
// Calling Mount twice on the same engine returns an *Error with
// CodeAlreadyMounted.
func (e *Engine[T]) Mount(router fiber.Router) error {
	if e.mounted {
		return &Error{Stage: "mount", Code: CodeAlreadyMounted, Message: "engine already mounted"}
	}
	if e.cfg == nil {
		return &Error{Stage: "mount", Code: CodeInvalidYAML, Message: "no YAML loaded — call LoadFile or LoadBytes first"}
	}

	plan, errs := e.buildPlan()
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	if err := e.installPlan(router, plan); err != nil {
		return err
	}

	e.mounted = true
	return nil
}

// plannedRoute is the fully resolved description of one route ready to be
// installed on Fiber.
type plannedRoute struct {
	Method, Path, Handler, Name, Description string
	Chain                                    []mwRef
	Tags                                     []string
}

// buildPlan walks the parsed YAML tree, resolves chains, and returns either a
// flat list of plannedRoute or a slice of accumulated *Error values.
func (e *Engine[T]) buildPlan() ([]plannedRoute, []error) {
	var errs []error
	var routes []plannedRoute
	seenRoute := map[string]string{}

	if e.builder == nil {
		errs = append(errs, &Error{Stage: "mount", Code: CodeMissingContextBuilder, Message: "ContextBuilder is required"})
	}

	var walk func(groups []rawGroup, prefix string, ancestors [][]mwRef, path string)
	walk = func(groups []rawGroup, prefix string, ancestors [][]mwRef, path string) {
		for i, g := range groups {
			gPath := fmt.Sprintf("%s[%d]", path, i)

			if g.MiddlewareSet != "" {
				if _, ok := e.cfg.MiddlewareSets[g.MiddlewareSet]; !ok {
					errs = append(errs, &Error{
						Stage: "mount", Code: CodeUnknownMiddlewareSet,
						Message: fmt.Sprintf("middleware_set %q is not defined", g.MiddlewareSet),
						Path:    gPath + ".middleware_set", File: e.cfgFile,
					})
				}
			}

			combined := combineSetAndList(g.MiddlewareSet, g.Middleware)
			fullPrefix := prefix + g.Prefix
			groupAncestors := append(append([][]mwRef{}, ancestors...), combined)

			for j, r := range g.Routes {
				rPath := fmt.Sprintf("%s.routes[%d]", gPath, j)

				if r.MiddlewareSet != "" {
					if _, ok := e.cfg.MiddlewareSets[r.MiddlewareSet]; !ok {
						errs = append(errs, &Error{
							Stage: "mount", Code: CodeUnknownMiddlewareSet,
							Message: fmt.Sprintf("middleware_set %q is not defined", r.MiddlewareSet),
							Path:    rPath + ".middleware_set", File: e.cfgFile,
						})
					}
				}

				routeMW := combineSetAndList(r.MiddlewareSet, r.Middleware)
				chain := resolveChain(e.cfg.MiddlewareSets, groupAncestors, routeMW)

				for _, ref := range chain {
					// nil Args = scalar form; non-nil (even empty) = map form (factory call).
					if ref.Args == nil {
						if _, ok := e.middlewares[ref.Name]; ok {
							continue
						}
						if _, ok := e.factories[ref.Name]; ok {
							errs = append(errs, &Error{
								Stage: "mount", Code: CodeUnknownMiddleware,
								Message: fmt.Sprintf("middleware %q is registered as a factory; YAML must use {%s: [...]} form", ref.Name, ref.Name),
								Path:    rPath + ".middleware", File: e.cfgFile,
							})
							continue
						}
						errs = append(errs, &Error{
							Stage: "mount", Code: CodeUnknownMiddleware,
							Message: fmt.Sprintf("middleware %q referenced from route is not registered", ref.Name),
							Path:    rPath + ".middleware", File: e.cfgFile,
						})
					} else {
						if _, ok := e.factories[ref.Name]; ok {
							continue
						}
						if _, ok := e.middlewares[ref.Name]; ok {
							errs = append(errs, &Error{
								Stage: "mount", Code: CodeUnknownMiddleware,
								Message: fmt.Sprintf("middleware %q is plain (no factory); YAML must reference it as a scalar string", ref.Name),
								Path:    rPath + ".middleware", File: e.cfgFile,
							})
							continue
						}
						errs = append(errs, &Error{
							Stage: "mount", Code: CodeUnknownMiddleware,
							Message: fmt.Sprintf("middleware factory %q referenced from route is not registered", ref.Name),
							Path:    rPath + ".middleware", File: e.cfgFile,
						})
					}
				}

				if _, ok := e.handlers[r.Handler]; !ok {
					errs = append(errs, &Error{
						Stage: "mount", Code: CodeUnknownHandler,
						Message: fmt.Sprintf("handler %q is not registered", r.Handler),
						Path:    rPath + ".handler", File: e.cfgFile,
					})
				}

				routePath := joinPath(fullPrefix, r.Path)
				key := r.Method + " " + routePath
				if prev, dup := seenRoute[key]; dup {
					errs = append(errs, &Error{
						Stage: "mount", Code: CodeDuplicateRoute,
						Message: fmt.Sprintf("route %s already defined (handler %s vs %s)", key, prev, r.Handler),
						Path:    rPath, File: e.cfgFile,
					})
					continue
				}
				seenRoute[key] = r.Handler

				routes = append(routes, plannedRoute{
					Method:      r.Method,
					Path:        routePath,
					Handler:     r.Handler,
					Name:        r.Name,
					Description: r.Description,
					Chain:       chain,
					Tags:        r.Tags,
				})
			}

			walk(g.Groups, fullPrefix, groupAncestors, gPath+".groups")
		}
	}
	walk(e.cfg.Groups, "", nil, "groups")
	return routes, errs
}

// combineSetAndList prepends the set name (as a synthetic mwRef) to the
// explicit list. resolveChain expands set names later.
func combineSetAndList(set string, list []mwRef) []mwRef {
	if set == "" {
		return list
	}
	out := make([]mwRef, 0, 1+len(list))
	out = append(out, mwRef{Name: set})
	out = append(out, list...)
	return out
}

func joinPath(prefix, path string) string {
	if path == "" {
		return prefix
	}
	if prefix == "" {
		return path
	}
	if prefix[len(prefix)-1] == '/' && path[0] == '/' {
		return prefix + path[1:]
	}
	return prefix + path
}

const ctxKey = "__fibermap_ctx__"

func (e *Engine[T]) installPlan(router fiber.Router, plan []plannedRoute) error {
	// Root middleware that builds the Context[T] and stashes it in locals.
	contextInit := func(c *fiber.Ctx) error {
		data, err := e.builder(c)
		if err != nil {
			return e.ctxError(c, err)
		}
		c.Locals(ctxKey, &Context[T]{Ctx: c, Data: data})
		return c.Next()
	}
	router.Use(contextInit)

	factoryCache := map[string]fiber.Handler{}

	for _, r := range plan {
		handlers := make([]fiber.Handler, 0, len(r.Chain)+1)
		for _, ref := range r.Chain {
			if ref.Args == nil {
				handlers = append(handlers, e.wrapMW(e.middlewares[ref.Name]))
				continue
			}
			key := dedupKey(ref)
			h, ok := factoryCache[key]
			if !ok {
				mw, err := e.factories[ref.Name](append([]string(nil), ref.Args...))
				if err != nil {
					return &Error{Stage: "mount", Code: CodeInvalidFactoryArgs,
						Message: fmt.Sprintf("factory %q rejected args %v: %s", ref.Name, ref.Args, err.Error()),
						File:    e.cfgFile}
				}
				h = e.wrapMW(mw)
				factoryCache[key] = h
			}
			handlers = append(handlers, h)
		}
		handlers = append(handlers, e.wrapHandler(e.handlers[r.Handler]))

		router.Add(r.Method, r.Path, handlers...)

		e.routes = append(e.routes, RouteInfo{
			Method:      r.Method,
			Path:        r.Path,
			Handler:     r.Handler,
			Name:        r.Name,
			Description: r.Description,
			Middleware:  toPublicChain(r.Chain),
			Tags:        r.Tags,
		})
	}
	return nil
}

func (e *Engine[T]) wrapMW(mw MiddlewareFunc[T]) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, ok := c.Locals(ctxKey).(*Context[T])
		if !ok {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		return mw(ctx)
	}
}

func (e *Engine[T]) wrapHandler(h HandlerFunc[T]) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, ok := c.Locals(ctxKey).(*Context[T])
		if !ok {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		return h(ctx)
	}
}

func toPublicChain(chain []mwRef) []MiddlewareRef {
	out := make([]MiddlewareRef, len(chain))
	for i, r := range chain {
		out[i] = MiddlewareRef{Name: r.Name, Args: append([]string(nil), r.Args...)}
	}
	return out
}

// Routes returns a snapshot of all routes registered during Mount.
// Returns an empty slice if called before Mount. The returned slice and
// each RouteInfo's slice fields are independent copies — mutating them
// will not affect engine state.
func (e *Engine[T]) Routes() []RouteInfo {
	out := make([]RouteInfo, len(e.routes))
	for i, r := range e.routes {
		out[i] = r
		out[i].Tags = append([]string(nil), r.Tags...)
		mw := make([]MiddlewareRef, len(r.Middleware))
		for j, m := range r.Middleware {
			mw[j] = MiddlewareRef{Name: m.Name, Args: append([]string(nil), m.Args...)}
		}
		out[i].Middleware = mw
	}
	return out
}