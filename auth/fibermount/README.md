# auth/fibermount

One-call bridge between `*auth.Auth[C]` and a `*fibermap.Engine[T]`. Registers the `bearer`, `require_scope`, and `require_role` factory middlewares onto the engine so routes.yaml can use them by name. The bridge lives in a subpackage so core `auth/` stays framework-agnostic (no fibermap import there).

**Parent:** [../README.md](../README.md)
**Import:** `github.com/theizzatbek/gokit/auth/fibermount`

## Use

```go
import (
    "github.com/theizzatbek/gokit/auth"
    "github.com/theizzatbek/gokit/auth/fibermount"
    "github.com/theizzatbek/gokit/fibermap"
)

eng := fibermap.New[AppCtx]()
authObj, _ := auth.New[MyClaims](cfg, auth.WithRefreshStore(store))

// One line — registers all three factory middlewares.
if err := fibermount.MountMiddlewareFactories(eng, authObj); err != nil {
    return err
}
```

Now in `routes.yaml`:

```yaml
groups:
  - prefix: /links
    middleware:
      - bearer: []
    routes:
      - method: GET
        path: ""
        handler: links.list
      - method: DELETE
        path: /:code
        handler: links.delete
        middleware:
          - require_role: [admin]
```

Both `bearer` (with `[]` = `BearerRequired`, `["optional"]` = `BearerOptional`) and `require_role`/`require_scope` (with arg lists) are now usable as YAML factory middlewares.

## Notes

- **`MountMiddlewareFactories` is the only public function** — does all three registrations at once. If you want only some of them, register the individual `*Factory` methods of `*auth.Auth[C]` via `fibermap.RegisterMiddlewareFactory` manually.
- **Bearer at fiber.App level vs. per-route:** when your engine's `ContextBuilder` reads the Bearer principal (typical), the auth check must run BEFORE `contextInit`. fibermount's `bearer: []` factory installs a per-route middleware that runs AFTER `contextInit` — too late for the builder. Solution: install `authObj.Bearer(auth.BearerOptional)` at fiber.App via `fibermap.WithUse(...)` so the principal is in Locals before the builder runs; per-route `bearer: []` then enforces 401 on protected paths.
- **`auth/` itself does NOT import `gokit/fibermap`.** Only this bridge does. Keeps `auth` usable from non-Fiber code (CLI, workers, scripts).
- **Adapts via `factory.Adapter/AdapterFactory`** under the hood (see [`fibermap/factory`](../../fibermap/factory/README.md)).

## See also

- [`auth`](../README.md) — parent: provides `Bearer`/`RequireScopeFactory`/`RequireRoleFactory` methods this bridge wraps
- [`fibermap`](../../fibermap/README.md) — `RegisterMiddlewareFactory`, `WithUse`
- [`examples/urlshort`](../../examples/urlshort/README.md) — uses fibermount end-to-end
