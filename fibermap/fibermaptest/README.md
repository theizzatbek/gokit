# fibermap/fibermaptest

Test helpers for asserting over `Engine.Routes()` — verify your routes.yaml registered the right methods, paths, handlers, middlewares, and tags. Pairs naturally with snapshot-style "route inventory" tests so deploys catch missing or renamed routes.

**Parent:** [../README.md](../README.md)
**Import:** `github.com/theizzatbek/gokit/fibermap/fibermaptest`

## Use

```go
import (
    "testing"
    "github.com/theizzatbek/gokit/fibermap/fibermaptest"
)

func TestRoutes(t *testing.T) {
    eng := buildEngine(t)   // your wiring helper
    if err := eng.LoadFile("routes.yaml"); err != nil { t.Fatal(err) }

    fibermaptest.AssertRoute(t, eng, "GET", "/v1/ping",
        fibermaptest.WithHandler("ping.get"),
        fibermaptest.WithTags("health"),
    )

    fibermaptest.AssertRoute(t, eng, "POST", "/v1/tasks",
        fibermaptest.WithHandler("tasks.create"),
        fibermaptest.WithMiddleware("bearer", "require_role"),
    )

    fibermaptest.AssertNoRoute(t, eng, "DELETE", "/v1/admin")
    fibermaptest.AssertRouteCount(t, eng, 12)  // catch accidental adds/removes
}
```

## Notes

- **`Engine.Routes()` is the source of truth.** These helpers walk that list — they don't drive HTTP requests. For request-level tests use `Engine.Mount(app)` + `app.Test(req)`.
- **`AssertRouteCount`** is a guard against silently-added routes. Set it once with `len(eng.Routes())` and re-run after intended changes.
- **`WithMiddleware(names ...)` matches as a substring set.** The route's middleware list must CONTAIN every name passed (order-agnostic, exact-name match). Use to catch a missing auth middleware on a protected route.
- **`WithHandler(name)`** matches the registered handler name (from `RegisterHandler` + the `handler:` field in YAML).
- **`TB` interface** accepts `*testing.T`, `*testing.B`, and anything else with `Errorf`/`Fatalf`/`Helper` — drop into benchmarks too.

## See also

- [`fibermap`](../README.md) — `Engine.Routes()` is what this asserts over
