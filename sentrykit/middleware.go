package sentrykit

import (
	"net/http"
	"net/url"

	"github.com/getsentry/sentry-go"
	"github.com/gofiber/fiber/v2"

	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
)

// hubKey is the Locals key under which the per-request *sentry.Hub
// is stored. Unexported by design — callers go through [HubFromContext].
type hubKey struct{}

// FiberMiddleware returns a fiber.Handler that scopes a fresh
// *sentry.Hub per request, pre-populates the scope with HTTP context
// (method, route template, headers, IP, request_id), and captures
// panics before re-panicking so the outer fibermap.Recover still
// writes the 500 response.
//
// Install as the first user middleware (service.WithSentry does this
// automatically). The order with fibermap's run.go install path is:
//
//	fibermap.Recover → RequestLogger → Metrics → otelfiber → sentry → user mw → handler
//
// A handler panic unwinds through `sentry`, which captures the
// exception with the per-request scope, then re-panics. The
// re-thrown panic propagates up through otelfiber/metrics/reqlog
// until fibermap.Recover catches it and renders 500.
func FiberMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		hub := sentry.CurrentHub().Clone()
		populateScope(c, hub.Scope())
		c.Locals(hubKey{}, hub)

		defer func() {
			// Fiber resolves the route only after the chain advances
			// past the global middleware that owns this defer — so we
			// update http.route here, on the way out, before any
			// panic-driven capture reads the scope.
			if route := c.Route(); route != nil && route.Path != "" {
				hub.Scope().SetTag("http.route", route.Path)
			}
			if r := recover(); r != nil {
				hub.RecoverWithContext(c.UserContext(), r)
				// Re-panic so fibermap.Recover (or fiber's stock
				// recover middleware) still owns the 500 response.
				panic(r)
			}
		}()
		return c.Next()
	}
}

// HubFromContext returns the request-scoped Sentry hub stored by
// [FiberMiddleware]. When the middleware isn't in the chain, falls
// back to sentry.CurrentHub() so callers can always emit — they just
// lose per-request scope.
//
// Side effect: when called after Fiber has resolved the matched route
// (i.e. from inside a handler), this lazily refreshes the hub's
// `http.route` tag. Fiber sets the route only after the global
// middleware chain that hosts FiberMiddleware has advanced past
// c.Next() — by the time a handler calls HubFromContext, the route
// is available and gets attached for any in-handler captures. The
// deferred panic path inside FiberMiddleware does the same refresh
// before calling RecoverWithContext.
func HubFromContext(c *fiber.Ctx) *sentry.Hub {
	hub, ok := c.Locals(hubKey{}).(*sentry.Hub)
	if !ok || hub == nil {
		return sentry.CurrentHub()
	}
	if route := c.Route(); route != nil && route.Path != "" {
		hub.Scope().SetTag("http.route", route.Path)
	}
	return hub
}

// WrapErrorHandler decorates inner so 5xx errors are captured to the
// per-request hub before delegating. Use it when the service sets a
// custom fiber error handler:
//
//	service.WithFiberConfig(fiber.Config{
//	    ErrorHandler: sentrykit.WrapErrorHandler(fibermap.ErrorHandler(logger)),
//	})
//
// Resolution uses [errs.HTTP] — the same status the inner handler
// will write — so the capture decision and the wire status stay
// consistent. Returns inner unchanged on 4xx/3xx/2xx outcomes.
func WrapErrorHandler(inner fiber.ErrorHandler) fiber.ErrorHandler {
	return func(c *fiber.Ctx, err error) error {
		status, _ := xerrs.HTTP(err)
		if status >= 500 {
			HubFromContext(c).CaptureException(err)
		}
		return inner(c, err)
	}
}

// populateScope writes the HTTP request context onto scope. Headers
// are copied verbatim (Sentry's PII scrubbing config applies on the
// server side — operators control redaction there, not here).
func populateScope(c *fiber.Ctx, scope *sentry.Scope) {
	req := &http.Request{
		Method: c.Method(),
		URL:    &url.URL{Path: c.Path(), RawQuery: string(c.Request().URI().QueryString())},
		Header: http.Header{},
		Host:   c.Hostname(),
		Proto:  c.Protocol(),
	}
	c.Request().Header.VisitAll(func(k, v []byte) {
		req.Header.Add(string(k), string(v))
	})
	scope.SetRequest(req)

	// http.route is set on the way out of FiberMiddleware (Fiber
	// resolves the matched route after the global middleware chain
	// advances); see the defer in FiberMiddleware.
	scope.SetTag("http.method", c.Method())
	if ip := c.IP(); ip != "" {
		scope.SetTag("client_ip", ip)
	}
	if rid, ok := c.Locals(fibermap.LocalsRequestID).(string); ok && rid != "" {
		scope.SetTag("request_id", rid)
	}
}