package fibermap

import (
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cache"
	"github.com/gofiber/fiber/v2/middleware/timeout"
	"github.com/theizzatbek/fibermap/bind"
)

type (
	HandlerFunc[T any]           func(c *Context[T]) error
	MiddlewareFunc[T any]        func(c *Context[T]) error
	MiddlewareFactoryFunc[T any] func(args []string) (MiddlewareFunc[T], error)
	ContextBuilder[T any]        func(c *fiber.Ctx) (T, error)
	ContextErrorFunc             func(c *fiber.Ctx, err error) error
)

// CacheDefaults holds the engine-wide knobs for the built-in response
// cache. Per-route TTL and flags live in the YAML `cache:` field;
// these defaults supply the bits that can't be expressed as strings.
type CacheDefaults[T any] struct {
	// Storage backs the cache. Any fiber.Storage implementation works:
	// the gofiber/storage repo ships drivers for Redis, memcached,
	// PostgreSQL, S3, …
	//
	// Nil → Fiber's default in-process map. Convenient for a single
	// instance; in production use a shared backend so replicas share
	// one cache and restarts don't wipe it.
	Storage fiber.Storage

	// KeyBy returns a per-request fragment mixed into every cache key
	// for routes that opt into caching. Use it to scope cache entries
	// by tenant / user / role — anything that should invalidate
	// independently.
	//
	// SECURITY: when nil, the cache key is method + URL + vary
	// headers. If you cache a handler whose response depends on the
	// authenticated user, you MUST set KeyBy or one user's response
	// will be served to another.
	KeyBy func(c *Context[T]) string

	// MaxBytes caps Fiber's default in-process store. Ignored when
	// Storage is set. 0 means unlimited — do not use in production.
	MaxBytes uint
}

// programmaticRoute is one route added via Engine.Add — outside the
// YAML route tree.
type programmaticRoute[T any] struct {
	Method, Path, Name, Description string
	Tags                            []string
	Handler                         HandlerFunc[T]
}

// Engine is the build-once-mount-once configurator. It is parameterized by T,
// the per-request payload type produced by ContextBuilder.
// BindErrorFunc renders a bind (parse / validate) error as an HTTP
// response. Returned from [Engine.SetBindErrorHandler]; the default
// emits a 400 with `{"error": err.Error()}`. Inspect specific
// failure modes via errors.Is against bind.ErrParse* / ErrValidate*
// sentinels.
type BindErrorFunc[T any] func(c *Context[T], err error) error

type Engine[T any] struct {
	builder     ContextBuilder[T]
	handlers    map[string]HandlerFunc[T]
	handlerMeta map[string]*HandlerMeta // opaque schemas from RegisterHandler options
	middlewares map[string]MiddlewareFunc[T]
	factories   map[string]MiddlewareFactoryFunc[T]
	ctxError    ContextErrorFunc

	validator bind.Validator
	bindError BindErrorFunc[T]

	cacheDefaults CacheDefaults[T]

	cfg     *rawConfig
	cfgFile string

	programmatic []programmaticRoute[T]

	// defaultRunOpts are prepended to user-supplied options inside
	// Run. fibermap.Default[T] populates this with the ops bundle so
	// `eng.Run()` ships with sensible production defaults.
	defaultRunOpts []RunOption

	routes  []RouteInfo
	lookup  map[string]int // method+" "+path → index into routes; built at Mount.
	mounted bool
}

// New constructs an empty Engine. Call SetContextBuilder, RegisterHandler,
// RegisterMiddleware (and optionally RegisterMiddlewareFactory) before
// LoadFile/LoadBytes and Mount.
func New[T any]() *Engine[T] {
	return &Engine[T]{
		handlers:    map[string]HandlerFunc[T]{},
		handlerMeta: map[string]*HandlerMeta{},
		middlewares: map[string]MiddlewareFunc[T]{},
		factories:   map[string]MiddlewareFactoryFunc[T]{},
		ctxError:    defaultContextError,
		bindError:   defaultBindError[T],
	}
}

// defaultBindError is the out-of-the-box rendering for parse /
// validate failures inside [RegisterBody] and friends: 400 with a
// JSON `{"error": "..."}` body containing the wrapped error
// message. Override via [Engine.SetBindErrorHandler].
func defaultBindError[T any](c *Context[T], err error) error {
	return c.Status(fiber.StatusBadRequest).JSON(map[string]string{"error": err.Error()})
}

// SetContextBuilder installs the function that builds the per-request Data.
// Calling more than once silently overwrites.
func (e *Engine[T]) SetContextBuilder(fn ContextBuilder[T]) { e.builder = fn }

// AddOpts collects the optional metadata for Engine.Add. Pass at most
// one to keep call sites readable:
//
//	eng.Add("GET", "/debug/pprof/heap", "debug.heap", pprofHeap,
//	    fibermap.AddOpts{Tags: []string{"debug", "ops"}})
type AddOpts struct {
	Description string
	Tags        []string
}

// Add registers a route programmatically — outside the YAML route
// tree. Useful for routes that don't fit the declarative model:
// debug/pprof endpoints, dynamic admin handlers, embedded UIs, etc.
//
// The handler goes through the same per-request Context[T] wrapper as
// YAML routes; the engine's root middleware (contextInit) still
// populates `Context[T].Data` for the request.
//
// Programmatic routes do NOT support middleware/timeout/cache via
// this API — those features are intentionally YAML-only to keep the
// declarative surface authoritative. If you need a programmatic route
// with middleware, install it directly on the fiber.App via
// WithConfigureApp.
//
// Routes added via Add are surfaced on Engine.Routes() with
// Source = SourceProgrammatic, so introspection and `fibermaptest`
// see them.
//
// Panics with *Error / CodeRegisterAfterMount if called after Mount.
// Panics with CodeInvalidHTTPMethod for unsupported methods. Empty
// name, empty path, or nil handler panic as a programmer error.
func (e *Engine[T]) Add(method, path, name string, h HandlerFunc[T], opts ...AddOpts) {
	if e.mounted {
		panic(&Error{Stage: "register", Code: CodeRegisterAfterMount,
			Message: "cannot Add route " + method + " " + path + " after Mount"})
	}
	if _, ok := validHTTPMethods[method]; !ok {
		panic(&Error{Stage: "register", Code: CodeInvalidHTTPMethod,
			Message: "Add: unsupported HTTP method " + method})
	}
	if name == "" {
		panic(&Error{Stage: "register", Code: CodeMissingField,
			Message: "Add: name is required (used for introspection / tests)"})
	}
	if path == "" {
		panic(&Error{Stage: "register", Code: CodeMissingField,
			Message: "Add: path is required"})
	}
	if h == nil {
		panic(&Error{Stage: "register", Code: CodeMissingField,
			Message: "Add: handler is nil"})
	}

	pr := programmaticRoute[T]{
		Method:  method,
		Path:    path,
		Name:    name,
		Handler: h,
	}
	if len(opts) > 0 {
		pr.Description = opts[0].Description
		pr.Tags = append([]string(nil), opts[0].Tags...)
	}
	e.programmatic = append(e.programmatic, pr)
}

// SetContextErrorHandler overrides the default response when ContextBuilder
// returns an error.
func (e *Engine[T]) SetContextErrorHandler(h ContextErrorFunc) { e.ctxError = h }

// SetValidator installs the validator used by [RegisterBody],
// [RegisterQuery], [RegisterParams], and [RegisterHeaders]. nil
// disables validation (the parse step still runs; the validate
// step is skipped — same behaviour as bind.Body with a nil
// validator).
//
//	eng.SetValidator(validator.New(validator.WithRequiredStructEnabled()))
func (e *Engine[T]) SetValidator(v bind.Validator) { e.validator = v }

// SetBindErrorHandler customises how parse / validate failures
// inside [RegisterBody] / [RegisterQuery] / [RegisterParams] /
// [RegisterHeaders] are turned into responses. Default returns
// 400 with `{"error": err.Error()}`.
//
// Inspect the error with errors.Is against bind.ErrParseBody /
// bind.ErrValidateBody (and the Query/Params/Header variants) to
// branch on parse vs validate.
func (e *Engine[T]) SetBindErrorHandler(fn BindErrorFunc[T]) {
	if fn == nil {
		fn = defaultBindError[T]
	}
	e.bindError = fn
}

// SetCacheDefaults installs engine-wide defaults for routes that
// declare `cache:` in YAML. Call once before Mount; later calls
// silently overwrite. See CacheDefaults for the security note about
// the KeyBy field.
func (e *Engine[T]) SetCacheDefaults(d CacheDefaults[T]) { e.cacheDefaults = d }

// panicIfMounted is called by every Register* method. Registering after
// Mount is silently useless — the registration map is consulted only
// during buildPlan/installPlan, both run by Mount — so we fail loud.
func (e *Engine[T]) panicIfMounted(kind, name string) {
	if e.mounted {
		panic(&Error{Stage: "register", Code: CodeRegisterAfterMount,
			Message: "cannot register " + kind + " " + name + " after Mount"})
	}
}

// RegisterHandler registers a handler under a name referenced from YAML.
// Panics with *Error / CodeDuplicateRegistration if the name is already
// taken, or CodeRegisterAfterMount if called after Mount.
//
// Optional [HandlerOption] values attach typed request / response
// schemas (read by fibermap/openapi for spec generation, ignored at
// runtime):
//
//	eng.RegisterHandler("tasks.create", h.Create,
//	    fibermap.WithBody(CreateReq{}),
//	    fibermap.WithResponse(201, Task{}),
//	    fibermap.WithResponse(400, ErrorResponse{}),
//	)
func (e *Engine[T]) RegisterHandler(name string, h HandlerFunc[T], opts ...HandlerOption) {
	e.panicIfMounted("handler", name)
	if _, ok := e.handlers[name]; ok {
		panic(&Error{Stage: "register", Code: CodeDuplicateRegistration, Message: "handler " + name + " already registered"})
	}
	e.handlers[name] = h
	if meta := applyHandlerOptions(opts); meta != nil {
		e.handlerMeta[name] = meta
	}
}

// HandlerMeta returns the schema metadata attached to a handler via
// [HandlerOption] values passed to [Engine.RegisterHandler]. Returns
// nil when no options were attached. The returned pointer aliases
// engine state — callers must treat it as read-only.
//
// Primarily for introspection consumers like fibermap/openapi.
func (e *Engine[T]) HandlerMeta(name string) *HandlerMeta {
	return e.handlerMeta[name]
}

// RegisterMiddleware registers a plain (no-args) middleware. YAML references
// it as a scalar string. Panics with *Error / CodeDuplicateRegistration if
// the name is already taken in either the plain or factory registry, or
// CodeRegisterAfterMount if called after Mount.
func (e *Engine[T]) RegisterMiddleware(name string, m MiddlewareFunc[T]) {
	e.panicIfMounted("middleware", name)
	if _, ok := e.middlewares[name]; ok {
		panic(&Error{Stage: "register", Code: CodeDuplicateRegistration, Message: "middleware " + name + " already registered"})
	}
	if _, ok := e.factories[name]; ok {
		panic(&Error{Stage: "register", Code: CodeDuplicateRegistration, Message: "name " + name + " already registered as a middleware factory"})
	}
	e.middlewares[name] = m
}

// RegisterMiddlewareFactory registers a parameterized middleware. YAML
// references it as a single-key mapping {name: [args...]}. The factory is
// invoked once per (name, args) pair at Mount time; the returned
// MiddlewareFunc is cached for the lifetime of the engine. Panics with
// *Error / CodeDuplicateRegistration on name conflict, or
// CodeRegisterAfterMount if called after Mount.
func (e *Engine[T]) RegisterMiddlewareFactory(name string, f MiddlewareFactoryFunc[T]) {
	e.panicIfMounted("middleware factory", name)
	if _, ok := e.factories[name]; ok {
		panic(&Error{Stage: "register", Code: CodeDuplicateRegistration, Message: "middleware factory " + name + " already registered"})
	}
	if _, ok := e.middlewares[name]; ok {
		panic(&Error{Stage: "register", Code: CodeDuplicateRegistration, Message: "name " + name + " already registered as a plain middleware"})
	}
	e.factories[name] = f
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

// LoadFS reads and parses a YAML file from an fs.FS — typically an
// embed.FS so the route definitions ship inside the binary.
//
//	//go:embed routes.yaml
//	var routesFS embed.FS
//	eng.LoadFS(routesFS, "routes.yaml")
func (e *Engine[T]) LoadFS(fsys fs.FS, path string) error {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return &Error{Stage: "parse", Code: CodeFileNotFound, Message: err.Error(), File: path}
	}
	cfg, err := parseBytes(data, path)
	if err != nil {
		return err
	}
	e.cfg = cfg
	e.cfgFile = path
	return nil
}

// Validate runs the same checks as Mount without installing any route
// on a Fiber router. Use it from CI scripts or tests to verify that a
// routes.yaml is consistent with the registered handlers, middleware,
// and factories. Returns the joined *Error values (errors.Join) or nil
// on success. Safe to call multiple times and at any point after a
// successful Load*.
func (e *Engine[T]) Validate() error {
	if e.cfg == nil {
		return &Error{Stage: "mount", Code: CodeInvalidYAML, Message: "no YAML loaded — call LoadFile, LoadBytes, or LoadFS first"}
	}
	_, errs := e.buildPlan()
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
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
		return &Error{Stage: "mount", Code: CodeInvalidYAML, Message: "no YAML loaded — call LoadFile, LoadBytes, or LoadFS first"}
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
	Method, Path, Handler, Name, Summary, Description string
	Chain                                             []mwRef
	Tags                                              []string
	Timeout                                           time.Duration
	TimeoutSpec                                       string
	Cache                                             *plannedCache
}

// plannedCache is the validated, parsed form of a route's cache YAML.
type plannedCache struct {
	TTL        time.Duration
	TTLSpec    string
	Control    bool
	Headers    bool
	VaryHeader []string
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

				// Timeout / cache TTL were format-validated in parseBytes;
				// these re-parses cannot fail.
				var timeoutDur time.Duration
				if r.Timeout != "" {
					timeoutDur, _ = time.ParseDuration(r.Timeout)
				}

				var pc *plannedCache
				if r.Cache != nil {
					ttl, _ := time.ParseDuration(r.Cache.TTL)
					pc = &plannedCache{
						TTL:        ttl,
						TTLSpec:    r.Cache.TTL,
						Control:    r.Cache.Control,
						Headers:    r.Cache.Headers,
						VaryHeader: append([]string(nil), r.Cache.VaryHeader...),
					}
				}

				routes = append(routes, plannedRoute{
					Method:      r.Method,
					Path:        routePath,
					Handler:     r.Handler,
					Name:        r.Name,
					Summary:     r.Summary,
					Description: r.Description,
					Chain:       chain,
					Tags:        r.Tags,
					Timeout:     timeoutDur,
					TimeoutSpec: r.Timeout,
					Cache:       pc,
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

// ctxKey is a unique sentinel used as the Fiber Locals key under
// which the per-request *Context[T] is stashed. A pointer is cheaper
// to compare than a string when Fiber walks the Locals slice on
// every Locals(key) call, and it eliminates any chance of a user
// accidentally setting a colliding string key.
var ctxKey = new(byte)

// ContextFrom retrieves the per-request *Context[T] that fibermap's
// root middleware stashed on the given *fiber.Ctx. Returns
// (nil, false) when called on a request that didn't pass through a
// fibermap-mounted router, or when T does not match the engine's
// payload type.
//
// Use this in code that operates on a plain fiber.Handler but still
// needs typed access to Context[T].Data — most commonly inside cache
// key generators or other adapters that bridge to non-fibermap
// middleware.
func ContextFrom[T any](c *fiber.Ctx) (*Context[T], bool) {
	ctx, ok := c.Locals(ctxKey).(*Context[T])
	return ctx, ok
}

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
		final := e.wrapHandler(e.handlers[r.Handler])
		if r.Timeout > 0 {
			final = timeout.NewWithContext(final, r.Timeout)
		}
		// Cache is inserted BEFORE the final handler: on hit it
		// responds without calling c.Next(); on miss it calls c.Next()
		// to invoke the handler and stores the response.
		if r.Cache != nil {
			handlers = append(handlers, e.cacheHandler(r.Cache))
		}
		handlers = append(handlers, final)

		router.Add(r.Method, r.Path, handlers...)

		e.routes = append(e.routes, RouteInfo{
			Method:      r.Method,
			Path:        r.Path,
			Handler:     r.Handler,
			Name:        r.Name,
			Summary:     r.Summary,
			Description: r.Description,
			Middleware:  toPublicChain(r.Chain),
			Tags:        r.Tags,
			Timeout:     r.TimeoutSpec,
			Cache:       toCacheInfo(r.Cache),
			Source:      SourceYAML,
		})
	}

	// Programmatic routes (Engine.Add): no middleware chain, no cache,
	// no timeout — just the typed handler going through the same
	// per-request Context[T] wrapper as YAML routes. They share the
	// route-conflict table so an Add() colliding with a YAML route
	// surfaces here as a Fiber-level panic (duplicate route).
	for _, pr := range e.programmatic {
		router.Add(pr.Method, pr.Path, e.wrapHandler(pr.Handler))
		e.routes = append(e.routes, RouteInfo{
			Method:      pr.Method,
			Path:        pr.Path,
			Handler:     pr.Name,
			Description: pr.Description,
			Tags:        append([]string(nil), pr.Tags...),
			Source:      SourceProgrammatic,
		})
	}

	// Build the (method, path) → index map for O(1) Lookup. The
	// routes slice stays the source of truth for Walk / Routes
	// (insertion order matters there); the map is just an
	// indexed-access side-channel.
	e.lookup = make(map[string]int, len(e.routes))
	for i, r := range e.routes {
		e.lookup[r.Method+" "+r.Path] = i
	}
	return nil
}

// cacheHandler returns the Fiber cache middleware configured from the
// route-level `cache:` block plus engine-wide CacheDefaults.
//
// The cache key is `METHOD ORIGINAL_URL` plus `|h:Name=value` for
// each VaryHeader plus a final `|d:fragment` for whatever
// CacheDefaults.KeyBy returns. KeyBy receives the per-request
// Context[T] populated by contextInit; if KeyBy is nil, the key
// depends only on method + URL + vary headers.
func (e *Engine[T]) cacheHandler(pc *plannedCache) fiber.Handler {
	defaults := e.cacheDefaults
	vary := pc.VaryHeader
	keyBy := defaults.KeyBy

	return cache.New(cache.Config{
		Expiration:           pc.TTL,
		CacheControl:         pc.Control,
		StoreResponseHeaders: pc.Headers,
		MaxBytes:             defaults.MaxBytes,
		Storage:              defaults.Storage,
		KeyGenerator: func(c *fiber.Ctx) string {
			var sb strings.Builder
			// Fiber's default key is c.Path() — that drops the method
			// and the query string, which is almost never what you
			// want. Use method + full URL as the base.
			sb.WriteString(c.Method())
			sb.WriteByte(' ')
			sb.WriteString(c.OriginalURL())
			for _, h := range vary {
				sb.WriteString("|h:")
				sb.WriteString(h)
				sb.WriteByte('=')
				sb.WriteString(c.Get(h))
			}
			if keyBy != nil {
				if ctx, ok := c.Locals(ctxKey).(*Context[T]); ok {
					if extra := keyBy(ctx); extra != "" {
						sb.WriteString("|d:")
						sb.WriteString(extra)
					}
				}
			}
			return sb.String()
		},
	})
}

func toCacheInfo(pc *plannedCache) *CacheInfo {
	if pc == nil {
		return nil
	}
	return &CacheInfo{
		TTL:        pc.TTLSpec,
		Control:    pc.Control,
		Headers:    pc.Headers,
		VaryHeader: append([]string(nil), pc.VaryHeader...),
	}
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
		out[i] = copyRouteInfo(r)
	}
	return out
}

// All returns a range-over-func iterator over every route registered
// during Mount, in Mount order. Each yielded RouteInfo is a defensive
// copy — safe to mutate without affecting engine state. Stop the
// iteration by returning from your loop body normally (break/return):
//
//	for r := range eng.All() {
//	    if strings.HasPrefix(r.Path, "/internal/") {
//	        continue
//	    }
//	    fmt.Println(r.Method, r.Path)
//	}
//
// Prefer All over Walk when targeting Go 1.23+ — it is idiomatic
// (no ErrStopWalk sentinel needed) and supports break / continue
// natively. Walk stays for callers that need an error-propagating
// callback shape.
func (e *Engine[T]) All() iter.Seq[RouteInfo] {
	return func(yield func(RouteInfo) bool) {
		for _, r := range e.routes {
			if !yield(copyRouteInfo(r)) {
				return
			}
		}
	}
}

// Walk invokes fn for every route registered during Mount, in Mount
// order. Returning ErrStopWalk from fn ends the walk without propagating
// an error; any other non-nil error from fn is returned to the caller.
// Each RouteInfo passed to fn is a defensive copy — safe to mutate.
//
// Walk is the building block for introspection consumers (OpenAPI
// generators, route-table CLIs, test helpers); use it instead of
// iterating Routes() when you might want to early-stop or filter.
// On Go 1.23+, prefer All() for idiomatic range-over-func.
func (e *Engine[T]) Walk(fn func(r RouteInfo) error) error {
	for _, r := range e.routes {
		if err := fn(copyRouteInfo(r)); err != nil {
			if err == ErrStopWalk {
				return nil
			}
			return err
		}
	}
	return nil
}

// Lookup returns the RouteInfo for a given (method, path) pair, or
// (zero, false) if no such route was registered. method is matched
// exactly (case-sensitive); path matches the resolved path (including
// any inherited prefix) exactly. The returned RouteInfo is a defensive
// copy.
//
// O(1) — the (method, path) → index map is built at Mount and consulted
// for every Lookup call. Walk and Routes still iterate the underlying
// slice in insertion order.
func (e *Engine[T]) Lookup(method, path string) (RouteInfo, bool) {
	if i, ok := e.lookup[method+" "+path]; ok {
		return copyRouteInfo(e.routes[i]), true
	}
	return RouteInfo{}, false
}

// ErrStopWalk may be returned by the function passed to Engine.Walk to
// stop iteration without surfacing an error to the caller.
var ErrStopWalk = errors.New("fibermap: stop walk")

func copyRouteInfo(r RouteInfo) RouteInfo {
	out := r
	out.Tags = append([]string(nil), r.Tags...)
	mw := make([]MiddlewareRef, len(r.Middleware))
	for j, m := range r.Middleware {
		mw[j] = MiddlewareRef{Name: m.Name, Args: append([]string(nil), m.Args...)}
	}
	out.Middleware = mw
	if r.Cache != nil {
		c := *r.Cache
		c.VaryHeader = append([]string(nil), r.Cache.VaryHeader...)
		out.Cache = &c
	}
	return out
}
