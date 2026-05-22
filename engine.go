package fibermap

import "github.com/gofiber/fiber/v2"

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
