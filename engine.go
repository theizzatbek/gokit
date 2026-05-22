package fibermap

import (
	"errors"
	"fmt"

	"github.com/gofiber/fiber/v2"
)

type (
	HandlerFunc[T any]    func(c *Context[T]) error
	MiddlewareFunc[T any] func(c *Context[T]) error
	ContextBuilder[T any] func(c *fiber.Ctx) (T, error)
	RoleChecker[T any]    func(c *Context[T], allowed []string) bool
	ForbiddenFunc[T any]  func(c *Context[T]) error
	ContextErrorFunc      func(c *fiber.Ctx, err error) error
)

// Engine is the build-once-mount-once configurator. It is parameterized by T,
// the per-request payload type produced by ContextBuilder.
type Engine[T any] struct {
	builder     ContextBuilder[T]
	handlers    map[string]HandlerFunc[T]
	middlewares map[string]MiddlewareFunc[T]
	roleChecker RoleChecker[T]
	forbidden   ForbiddenFunc[T]
	ctxError    ContextErrorFunc

	cfg     *rawConfig
	cfgFile string

	routes  []RouteInfo
	mounted bool
}

// New constructs an empty Engine. Call SetContextBuilder, RegisterHandler,
// RegisterMiddleware, and (if YAML uses `roles:`) SetRoleChecker before
// LoadFile/LoadBytes and Mount.
func New[T any]() *Engine[T] {
	return &Engine[T]{
		handlers:    map[string]HandlerFunc[T]{},
		middlewares: map[string]MiddlewareFunc[T]{},
		forbidden:   defaultForbidden[T],
		ctxError:    defaultContextError,
	}
}

// SetContextBuilder installs the function that builds the per-request Data.
// Calling more than once silently overwrites.
func (e *Engine[T]) SetContextBuilder(fn ContextBuilder[T]) { e.builder = fn }

// SetRoleChecker installs the role authorization function. Required if any
// route declares `roles:` in YAML.
func (e *Engine[T]) SetRoleChecker(fn RoleChecker[T]) { e.roleChecker = fn }

// SetForbiddenHandler overrides the default 403 response.
func (e *Engine[T]) SetForbiddenHandler(h ForbiddenFunc[T]) { e.forbidden = h }

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

// RegisterMiddleware registers a middleware under a name referenced from YAML.
// Returns *Error with CodeDuplicateRegistration if the name is already taken.
func (e *Engine[T]) RegisterMiddleware(name string, m MiddlewareFunc[T]) error {
	if _, ok := e.middlewares[name]; ok {
		return &Error{Stage: "register", Code: CodeDuplicateRegistration, Message: "middleware " + name + " already registered"}
	}
	e.middlewares[name] = m
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

func defaultForbidden[T any](c *Context[T]) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "forbidden"})
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
	Chain       []string // resolved middleware names (may include roleGuardName)
	Roles       []string
	Tags        []string
}

// buildPlan walks the parsed YAML tree, resolves chains, and returns either a
// flat list of plannedRoute or a slice of accumulated *Error values.
func (e *Engine[T]) buildPlan() ([]plannedRoute, []error) {
	var errs []error
	var routes []plannedRoute
	seenRoute := map[string]string{} // "METHOD path" -> handler name (for diagnostics)

	if e.builder == nil {
		errs = append(errs, &Error{Stage: "mount", Code: CodeMissingContextBuilder, Message: "ContextBuilder is required"})
	}

	var walk func(groups []rawGroup, prefix string, ancestors [][]string, path string)
	walk = func(groups []rawGroup, prefix string, ancestors [][]string, path string) {
		for i, g := range groups {
			gPath := fmt.Sprintf("%s[%d]", path, i)

			// Fix 1 (group-level): validate that the referenced middleware_set exists.
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
			groupAncestors := append(append([][]string{}, ancestors...), combined)

			for j, r := range g.Routes {
				rPath := fmt.Sprintf("%s.routes[%d]", gPath, j)

				// Fix 1 (route-level): validate that the referenced middleware_set exists.
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

				chain, _ := resolveChain(e.cfg.MiddlewareSets, groupAncestors, routeMW, len(r.Roles) > 0)

				// validate every chain entry exists (set names get expanded; only
				// concrete middleware names should remain — roleGuardName is allowed).
				for _, name := range chain {
					if name == roleGuardName {
						continue
					}
					if _, ok := e.middlewares[name]; !ok {
						errs = append(errs, &Error{
							Stage: "mount", Code: CodeUnknownMiddleware,
							Message: fmt.Sprintf("middleware %q referenced from route is not registered", name),
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

				if len(r.Roles) > 0 && e.roleChecker == nil {
					errs = append(errs, &Error{
						Stage: "mount", Code: CodeMissingRoleChecker,
						Message: fmt.Sprintf("route uses roles %v but RoleChecker is not set", r.Roles),
						Path:    rPath + ".roles", File: e.cfgFile,
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
					Roles:       r.Roles,
					Tags:        r.Tags,
				})
			}

			walk(g.Groups, fullPrefix, groupAncestors, gPath+".groups")
		}
	}
	walk(e.cfg.Groups, "", nil, "groups")
	return routes, errs
}

// combineSetAndList returns set name (if any) prepended to the explicit list.
// resolveChain expands set names later.
func combineSetAndList(set string, list []string) []string {
	if set == "" {
		return list
	}
	out := make([]string, 0, 1+len(list))
	out = append(out, set)
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
	// don't double up slashes
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

	for _, r := range plan {
		handlers := make([]fiber.Handler, 0, len(r.Chain)+1)
		for _, name := range r.Chain {
			if name == roleGuardName {
				roles := append([]string{}, r.Roles...) // capture
				handlers = append(handlers, e.wrapRoleGuard(roles))
				continue
			}
			mw := e.middlewares[name]
			handlers = append(handlers, e.wrapMW(mw))
		}
		handlers = append(handlers, e.wrapHandler(e.handlers[r.Handler]))

		router.Add(r.Method, r.Path, handlers...)

		e.routes = append(e.routes, RouteInfo{
			Method:      r.Method,
			Path:        r.Path,
			Handler:     r.Handler,
			Name:        r.Name,
			Description: r.Description,
			Middleware:  filterOutSentinel(r.Chain),
			Roles:       r.Roles,
			Tags:        r.Tags,
		})
	}
	return nil
}

func (e *Engine[T]) wrapMW(mw MiddlewareFunc[T]) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, ok := c.Locals(ctxKey).(*Context[T])
		if !ok {
			// contextInit didn't run — fail loudly rather than silently bypass.
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

func (e *Engine[T]) wrapRoleGuard(allowed []string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, ok := c.Locals(ctxKey).(*Context[T])
		if !ok {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		if !e.roleChecker(ctx, allowed) {
			return e.forbidden(ctx)
		}
		return c.Next()
	}
}

func filterOutSentinel(chain []string) []string {
	out := make([]string, 0, len(chain))
	for _, n := range chain {
		if n == roleGuardName {
			continue
		}
		out = append(out, n)
	}
	return out
}
