# gokit

A composable Go service kit. Nine independently importable packages that cover
what every HTTP API service hand-rolls: routing, errors, database, auth,
outbound HTTP, declarative outbound APIs, NATS event streaming, declarative
NATS subscribers + publishers.

Each package can be adopted standalone. Together they take you from
`main.go` to a production-shaped service.

**Status:** 0.x — API unstable.

## Packages

| Path | What it does |
|------|---|
| `fibermap/` | YAML-declarative router for Fiber v2. Handlers/middleware by name. Typed per-request context. OpenAPI generation. |
| `errs/` | Typed domain errors (`Kind`, `Code`, `Details`, `Cause`) with HTTP mapping. Stdlib-only. |
| `db/` | pgx-based pool wrapper. Transactions with savepoints. Healthcheck. `*errs.Error` mapping. |
| `db/sqb/` | Opt-in squirrel wrapper preconfigured for `$N` placeholders. |
| `auth/` | JWT issue/verify (EdDSA/ES256). Argon2id hashing. Refresh-token rotation. Ready-to-mount Fiber middleware. |
| `clients/httpc/` | Outbound `*http.Client` builder. Retry, per-attempt timeout, slog + Prometheus observability. |
| `clients/apimap/` | Declarative outbound: describe upstream APIs in YAML, call them by name. Auth and `${ENV_VAR}` secrets in YAML. |
| `clients/nats/` | Typed JetStream wrapper. Generic `Publisher[T]` / `Subscribe[T]`. Auto-ack handler model. |
| `clients/natsmap/` | Declarative NATS subscribers + publishers via YAML. Typed handlers + publishers by name, `*Runtime.Drain()` for graceful shutdown. |
| `service/` | Optional all-in-one helper. `service.New(ctx, cfg)` bundles every other subpackage into a `Service[T, C]` runtime with auto-detect optionality, auto-mounted auth handlers, and the Bearer-optional layer fix. Shrinks `main.go` for typical services from ~270 → ~80 lines. |

## Dependency rules

```
errs                      → stdlib only
db, db/sqb                → errs + pgx
clients/httpc             → errs + prometheus
clients/apimap            → errs + clients/httpc + yaml.v3
clients/nats              → errs + nats.go + prometheus
clients/natsmap           → errs + clients/nats + yaml.v3
auth                      → errs + crypto + jwt + fiber
fibermap                  → errs + fiber (router-adjacent sub-packages only)
```

Root `gokit` package is empty — no exported symbols. Importing one
subpackage does not pull the others.

## Install

```bash
go get github.com/theizzatbek/gokit/fibermap
go get github.com/theizzatbek/gokit/errs
go get github.com/theizzatbek/gokit/db
go get github.com/theizzatbek/gokit/auth
go get github.com/theizzatbek/gokit/clients/httpc
go get github.com/theizzatbek/gokit/clients/apimap
go get github.com/theizzatbek/gokit/clients/nats
go get github.com/theizzatbek/gokit/clients/natsmap

# optional: standalone CLI for routes.yaml linting and schema export
go install github.com/theizzatbek/gokit/cmd/fibermap@latest
```

Requires Go 1.23+ and (for `fibermap/`) Fiber v2.

## Quickstart — fibermap router

```yaml
# routes.yaml
groups:
  - prefix: /v1
    routes:
      - method: GET
        path:   /ping
        handler: ping
        name:   ping.get
```

```go
// main.go
package main

import (
    "context"

    "github.com/gofiber/fiber/v2"
    "github.com/theizzatbek/gokit/fibermap"
)

type AppCtx struct{ /* per-request data */ }

func main() {
    eng := fibermap.New[AppCtx]()
    eng.SetContextBuilder(func(c *fiber.Ctx) (AppCtx, error) {
        return AppCtx{}, nil
    })

    fibermap.RegisterHandler(eng, "ping", func(c *fibermap.Context[AppCtx]) error {
        return c.SendString("pong")
    })

    if err := eng.LoadFile("routes.yaml"); err != nil {
        panic(err)
    }

    if err := eng.Run(context.Background(), fibermap.WithAddr(":3000")); err != nil {
        panic(err)
    }
}
```

## Editor support for `routes.yaml`

Add this line at the top of your `routes.yaml`:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/theizzatbek/gokit/main/fibermap/schema/routes.schema.json
```

VS Code (with [redhat.vscode-yaml]), GoLand, and Vim with `coc-yaml`
give autocomplete for `method` / `middleware_sets` / etc, hover docs,
and inline diagnostics — typos in `middleware:` get highlighted before
you ever run `go test`.

[redhat.vscode-yaml]: https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml

## CLI

```bash
fibermap validate routes.yaml    # schema-lint; non-zero exit on issues
fibermap dump-schema             # print the bundled JSON Schema
```

`validate` runs schema-level checks (required fields, valid HTTP methods,
middleware_set cycles, middleware shape). It does NOT verify that
handler/middleware/factory names are registered — your Go binary is the
only place those live. For full validation (including registrations),
call `Engine.Validate()` in a Go test or boot script.

## Examples

- `examples/quickstart/` — minimal Hello-world
- `examples/auth/` — JWT login + Bearer middleware
- `examples/nats/` — typed publisher / subscriber
- `examples/tasks/` — fuller service (config, db, auth, OpenAPI)
