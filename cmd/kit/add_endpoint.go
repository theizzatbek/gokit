package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const usageAddEndpoint = `kit add-endpoint — append a route + handler stub

Usage:
  kit add-endpoint METHOD PATH HANDLER [--routes configs/routes.yaml]
                                       [--handlers-pkg internal/handlers]
                                       [--group ""]

Examples:
  kit add-endpoint GET /tasks tasks.list
  kit add-endpoint POST /tasks tasks.create --group /api/v1

Effects:
  - Appends a route entry to routes.yaml (under the matching group
    prefix, or creates one when --group is non-empty).
  - Creates a handler stub at HANDLERS_PKG/<handler-base>.go if the
    file does not exist, or appends a stub function when it does.

The handler name uses the kit convention "<group>.<verb>" (dot-
separated). The dot is conventional only — the YAML registry treats
the whole string as the handler key.
`

// supportedMethods mirrors fibermap's accepted method set; the
// scaffolder rejects unknown verbs at parse-time rather than
// surfacing a parse-stage error at runtime.
var supportedMethods = map[string]bool{
	"GET":     true,
	"POST":    true,
	"PUT":     true,
	"PATCH":   true,
	"DELETE":  true,
	"HEAD":    true,
	"OPTIONS": true,
}

func runAddEndpoint(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("add-endpoint", flag.ContinueOnError)
	routesPath := fs.String("routes", "configs/routes.yaml", "path to routes.yaml")
	handlersDir := fs.String("handlers-pkg", "internal/handlers", "directory for handler stubs")
	group := fs.String("group", "", "group prefix under which to add the route (default: top-level)")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usageAddEndpoint) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 3 {
		fs.Usage()
		return errors.New("METHOD PATH HANDLER are required")
	}
	method := strings.ToUpper(fs.Arg(0))
	path := fs.Arg(1)
	handler := fs.Arg(2)
	if !supportedMethods[method] {
		return fmt.Errorf("unsupported METHOD %q (want GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS)", method)
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("PATH must start with '/'; got %q", path)
	}
	if handler == "" {
		return errors.New("HANDLER is required")
	}

	if err := appendRouteToYAML(*routesPath, method, path, handler, *group); err != nil {
		return fmt.Errorf("update routes.yaml: %w", err)
	}
	if err := ensureHandlerStub(*handlersDir, handler); err != nil {
		return fmt.Errorf("scaffold handler: %w", err)
	}
	fmt.Printf("kit add-endpoint: %s %s → %s\n", method, path, handler)
	fmt.Printf("  routes.yaml: appended\n")
	fmt.Printf("  %s.go: handler stub ensured\n", filepath.Join(*handlersDir, handlerFileBase(handler)))
	return nil
}

// appendRouteToYAML writes a new route entry under the matching
// group. Implemented as text-append (NOT yaml-parse-rewrite) so we
// don't have to round-trip every comment + ordering the operator
// might have placed in the file. Kit's YAML schema is forgiving —
// the engine deduplicates groups by prefix at LoadBytes time.
func appendRouteToYAML(path, method, route, handler, group string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("%s not found — run from the service root or pass --routes", path)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := group
	if prefix == "" {
		prefix = "/"
	}
	entry := fmt.Sprintf(`
# Appended by `+"`"+`kit add-endpoint`+"`"+`
groups:
  - prefix: %s
    routes:
      - method: %s
        path: %s
        handler: %s
`, prefix, method, route, handler)
	_, err = f.WriteString(entry)
	return err
}

// ensureHandlerStub writes a Go file with a stub Register function +
// handler closure. The kit convention is one file per group; the
// scaffolder names the file by the part before the first '.' in the
// handler name (e.g. handler "tasks.list" → file "tasks.go").
//
// Existing files are NOT clobbered — the scaffolder appends a new
// stub function instead, keeping prior handlers in place.
func ensureHandlerStub(handlersDir, handler string) error {
	if err := os.MkdirAll(handlersDir, 0o755); err != nil {
		return err
	}
	base := handlerFileBase(handler)
	dest := filepath.Join(handlersDir, base+".go")
	stub := buildHandlerStub(base, handler)
	if _, err := os.Stat(dest); err != nil {
		// New file → write a self-contained module.
		body := buildHandlerFile(base, handler)
		return os.WriteFile(dest, []byte(body), 0o644)
	}
	// Existing file → append a registration-call comment so the
	// operator wires it up explicitly. We don't try to merge into
	// the existing Register(svc) function because operators have
	// strong preferences about handler ordering / grouping.
	f, err := os.OpenFile(dest, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("\n// TODO: register handler " + handler + " in this file:\n//\n" + stub + "\n")
	return err
}

// handlerFileBase returns the part of the handler name before the
// first '.', falling back to the whole name when no dot is present.
func handlerFileBase(handler string) string {
	if i := strings.Index(handler, "."); i > 0 {
		return handler[:i]
	}
	return handler
}

func buildHandlerStub(_ string, handler string) string {
	return fmt.Sprintf(`//   fibermap.RegisterHandler(svc.Engine, %q, func(c *fibermap.Context[T]) error {
//       return c.Ctx.SendStatus(200)
//   })`, handler)
}

func buildHandlerFile(base, handler string) string {
	return fmt.Sprintf(`package handlers

import (
	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/fibermap"
	"github.com/theizzatbek/gokit/service"
)

// Register%s wires this group's handlers onto the service's engine.
// Call from your Register(svc) entry point.
func Register%s[T any, C any](svc *service.Service[T, C]) {
	fibermap.RegisterHandler(svc.Engine, %q, func(c *fibermap.Context[T]) error {
		// TODO: implement %s
		return c.Ctx.Status(fiber.StatusOK).JSON(map[string]any{
			"handler": %q,
		})
	})
}
`, titleCase(base), titleCase(base), handler, handler, handler)
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
