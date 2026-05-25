// Package fibermaptest provides assertion helpers for fibermap engines.
//
// The helpers operate on the public introspection API (Walk/Lookup/Routes)
// and never require spinning up a Fiber app — so tests can assert
// "route X exists with middleware Y" without a live server.
//
// Typical use:
//
//	func TestRoutesYAML(t *testing.T) {
//	    eng := buildEngineForTests(t)  // calls Load* + Mount on a throwaway router
//	    fibermaptest.AssertRoute(t, eng, "POST", "/v1/things",
//	        fibermaptest.WithHandler("things.create"),
//	        fibermaptest.WithMiddleware("auth"),
//	    )
//	}
package fibermaptest

import "github.com/theizzatbek/gokit/fibermap"

// RouteFinder is the minimal contract every fibermap.Engine[T] satisfies.
// Helpers in this package take a RouteFinder so they work uniformly across
// any concrete payload type T.
type RouteFinder interface {
	Lookup(method, path string) (fibermap.RouteInfo, bool)
	Walk(fn func(fibermap.RouteInfo) error) error
}

// TB is the minimal testing surface used by these helpers — satisfied
// by both *testing.T and *testing.B. Helpers call t.Helper() and
// t.Errorf(...) only; they never fail-fast (t.Fatal), so callers can
// stack multiple assertions and see all failures at once.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
}

// Option configures an AssertRoute call.
type Option func(*assertion)

type assertion struct {
	handler    string
	hasHandler bool
	middleware []string
	hasTags    []string
}

// WithHandler asserts the route resolves to the handler registered under
// the given name.
func WithHandler(name string) Option {
	return func(a *assertion) {
		a.handler = name
		a.hasHandler = true
	}
}

// WithMiddleware asserts the route's resolved chain contains the given
// middleware names in the given relative order. Other middleware entries
// may appear between them. Factory args are ignored — only the Name is
// matched. Use the explicit chain inspection if you need to match args.
func WithMiddleware(names ...string) Option {
	return func(a *assertion) {
		a.middleware = append(a.middleware, names...)
	}
}

// WithTags asserts each given tag appears in the route's Tags slice.
func WithTags(tags ...string) Option {
	return func(a *assertion) {
		a.hasTags = append(a.hasTags, tags...)
	}
}

// AssertRoute fails the test if no route is registered for (method, path)
// or if any provided Option doesn't hold.
func AssertRoute(t TB, eng RouteFinder, method, path string, opts ...Option) {
	t.Helper()
	r, ok := eng.Lookup(method, path)
	if !ok {
		t.Errorf("fibermaptest: no route registered for %s %s", method, path)
		return
	}
	a := assertion{}
	for _, opt := range opts {
		opt(&a)
	}
	if a.hasHandler && r.Handler != a.handler {
		t.Errorf("fibermaptest: %s %s handler = %q, want %q", method, path, r.Handler, a.handler)
	}
	if len(a.middleware) > 0 {
		if missing := missingInOrder(a.middleware, mwNames(r.Middleware)); len(missing) > 0 {
			t.Errorf("fibermaptest: %s %s middleware chain %v missing %v (in order)",
				method, path, mwNames(r.Middleware), missing)
		}
	}
	if len(a.hasTags) > 0 {
		if missing := missingAny(a.hasTags, r.Tags); len(missing) > 0 {
			t.Errorf("fibermaptest: %s %s tags %v missing %v",
				method, path, r.Tags, missing)
		}
	}
}

// AssertNoRoute fails the test if a route IS registered for (method, path).
// Useful to verify negative cases: "POST /admin must NOT be exposed in
// the public router".
func AssertNoRoute(t TB, eng RouteFinder, method, path string) {
	t.Helper()
	if r, ok := eng.Lookup(method, path); ok {
		t.Errorf("fibermaptest: %s %s should not be registered, got handler %q",
			method, path, r.Handler)
	}
}

// AssertRouteCount fails the test if the total number of registered
// routes is not n. Handy for "no routes leaked / added without my
// noticing" tests.
func AssertRouteCount(t TB, eng RouteFinder, n int) {
	t.Helper()
	got := 0
	_ = eng.Walk(func(r fibermap.RouteInfo) error {
		got++
		return nil
	})
	if got != n {
		t.Errorf("fibermaptest: route count = %d, want %d", got, n)
	}
}

func mwNames(refs []fibermap.MiddlewareRef) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.Name
	}
	return out
}

// missingInOrder returns the entries from `wanted` that don't appear in
// `got` in the same relative order. Other entries in `got` are allowed
// between matches.
func missingInOrder(wanted, got []string) []string {
	i := 0
	for _, g := range got {
		if i < len(wanted) && g == wanted[i] {
			i++
		}
	}
	if i == len(wanted) {
		return nil
	}
	return wanted[i:]
}

// missingAny returns entries from `wanted` that don't appear in `got`.
func missingAny(wanted, got []string) []string {
	gotSet := make(map[string]struct{}, len(got))
	for _, g := range got {
		gotSet[g] = struct{}{}
	}
	var missing []string
	for _, w := range wanted {
		if _, ok := gotSet[w]; !ok {
			missing = append(missing, w)
		}
	}
	return missing
}
