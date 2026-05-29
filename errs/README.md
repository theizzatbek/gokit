# errs

Typed domain errors with HTTP mapping. One `*Error` type carries a closed-enum `Kind`, a stable string `Code`, optional `Details` for field-level failures, and a wrapped `Cause`. `errs.HTTP(err)` maps to the wire shape every kit package agrees on.

**Import:** `github.com/theizzatbek/gokit/errs`
**Depends on:** stdlib only

## Why use it

Every service ends up inventing the same vocabulary: "not found", "validation failed", "unauthorized", "internal". `errs` is that vocabulary, plus an HTTP mapping table so `return err` from a handler produces the right status + JSON body without each handler re-deciding. Every other gokit package returns `*errs.Error` for known conditions — so the contract is uniform across `db`, `auth`, `clients/*`, `fibermap`.

## Quickstart

```go
import (
    "errors"
    xerrs "github.com/theizzatbek/gokit/errs"
)

// Constructing
if user == nil {
    return xerrs.NotFound("user_not_found", "user does not exist")
}
if err := svc.charge(); err != nil {
    return xerrs.Wrap(err, xerrs.KindUnavailable, "payment_provider_down", "stripe call failed")
}

// Consuming (e.g. in fibermap's ErrorHandler — already wired by fibermap.ErrorHandler)
status, body := xerrs.HTTP(err)  // status=404, body={code:"user_not_found", ...}
return c.Status(status).JSON(body)
```

## The `Kind` taxonomy

| Kind | HTTP status | Use when |
|---|---|---|
| `KindNotFound` | 404 | Resource doesn't exist |
| `KindAlreadyExists` | 409 | Unique-key collision on create |
| `KindConflict` | 409 | Generic write conflict (concurrent edit, fk violation) |
| `KindValidation` | 400 | Bad input — bad shape, failed validation rules |
| `KindUnauthorized` | 401 | No / invalid credentials |
| `KindPermission` | 403 | Authenticated but not allowed |
| `KindRateLimited` | 429 | Too many requests |
| `KindUnavailable` | 503 | Dependency down (DB, upstream) |
| `KindTimeout` | 504 | Operation exceeded deadline |
| `KindInternal` | 500 | Programmer error / unrecognised failure |
| `KindUnknown` (zero value) | 500 | Don't construct directly; means "not classified" |

`HTTP(err)` returns 500 for any `error` that isn't `*errs.Error` — so unhandled errors fail safe instead of leaking internals.

## Constructors

Every `Kind` has two flavours:

| Kind | Plain | Sprintf |
|---|---|---|
| NotFound | `errs.NotFound(code, msg)` | `errs.NotFoundf(code, format, args...)` |
| AlreadyExists | `errs.AlreadyExists(code, msg)` | `errs.AlreadyExistsf(...)` |
| Conflict | `errs.Conflict(code, msg)` | `errs.Conflictf(...)` |
| Validation | `errs.Validation(code, msg, details...)` | `errs.Validationf(code, format, args...)` |
| Unauthorized | `errs.Unauthorized(code, msg)` | `errs.Unauthorizedf(...)` |
| Permission | `errs.Permission(code, msg)` | `errs.Permissionf(...)` |
| RateLimited | `errs.RateLimited(code, msg)` | `errs.RateLimitedf(...)` |
| Unavailable | `errs.Unavailable(code, msg)` | `errs.Unavailablef(...)` |
| Timeout | `errs.Timeout(code, msg)` | `errs.Timeoutf(...)` |
| Internal | `errs.Internal(code, msg)` | `errs.Internalf(...)` |

Wrap an existing `error`:

```go
errs.Wrap(err, errs.KindInternal, "db_failure", "database operation failed")
errs.Wrapf(err, errs.KindUnavailable, "stripe_call_failed", "stripe %s call failed", op)
```

`errors.Unwrap`, `errors.Is`, `errors.As` all work on wrapped errors.

## Common patterns

### Code naming convention

Stable, machine-readable, lowercase snake_case. Per-package or per-domain prefix avoids collisions:

```
user_not_found          // generic
auth_invalid_credentials
db_failure
apimap_github_get_user_not_found  // generated per-endpoint by clients/apimap
```

Codes are public API for downstream consumers (they may switch on them in their tests or alerting). Treat code changes like API changes.

### FieldError details for validation

```go
errs.Validation("invalid_body", "request body failed validation",
    errs.FieldError{Field: "email", Rule: "required", Message: "email is required"},
    errs.FieldError{Field: "age", Rule: "min", Param: "18", Message: "must be at least 18"},
)
```

Wire shape:

```json
{
  "code": "invalid_body",
  "message": "request body failed validation",
  "details": [
    {"field": "email", "rule": "required", "message": "email is required"},
    {"field": "age", "rule": "min", "param": "18", "message": "must be at least 18"}
  ]
}
```

For converting `go-playground/validator` `ValidationErrors` automatically, use [`errs/errsval`](errsval/README.md).

### Attaching details after construction

```go
e := errs.Validation("invalid_body", "bad request").
    WithDetails(
        errs.FieldError{Field: "x", Rule: "required"},
    )
```

### Inspecting errors

```go
var e *xerrs.Error
if errors.As(err, &e) {
    switch e.Kind {
    case xerrs.KindNotFound:   // …
    case xerrs.KindValidation: // …
    }
    log.Info("known failure", "code", e.Code)
}

// or by Kind:
if errors.Is(err, somethingSentinel) { /* … */ }

// unwrap to original cause:
var pgErr *pgconn.PgError
if errors.As(err, &pgErr) { /* … */ }
```

### Flattening `errors.Join` aggregates

Kit build/validate steps return `errors.Join(...)` when multiple
`*xerrs.Error` failures co-occur. `errs.All` walks the join tree
(plus standard `Unwrap` chains) and hands back every `*Error` it can
reach, in depth-first order:

```go
if err := eng.Mount(app); err != nil {
    for _, e := range xerrs.All(err) {
        log.Warn("mount issue", "code", e.Code, "kind", e.Kind, "msg", e.Message)
    }
    return err
}
```

Wrapped chains (`Wrap(rootErr, ...)`) surface both layers. Non-`*Error`
members of a Join are skipped silently; `errs.All(nil)` returns `nil`.

### Structured logging

`*Error` implements `slog.LogValuer`, so passing it to `slog` emits structured fields automatically:

```go
logger.Error("create user failed", "err", err)
// → {"level":"ERROR","msg":"create user failed",
//    "err":{"kind":"validation","code":"user_exists","message":"…"}}
```

## HTTP integration

`errs.HTTP(err) (int, Response)` is the single function fibermap's `ErrorHandler` calls. You almost never call it directly — register `fibermap.ErrorHandler(logger)` as your `fiber.Config.ErrorHandler` and `return err` from handlers.

For non-fibermap servers (e.g. stdlib `net/http`):

```go
func handle(w http.ResponseWriter, r *http.Request) {
    err := doWork()
    if err != nil {
        status, body := xerrs.HTTP(err)
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(status)
        json.NewEncoder(w).Encode(body)
        return
    }
    // …
}
```

## Testing

Match Kind + Code precisely:

```go
err := svc.Delete(ctx, "missing")
var e *xerrs.Error
if !errors.As(err, &e) {
    t.Fatalf("err = %v (type %T), want *errs.Error", err, err)
}
if e.Kind != xerrs.KindNotFound {
    t.Errorf("Kind = %v, want NotFound", e.Kind)
}
if e.Code != "user_not_found" {
    t.Errorf("Code = %q", e.Code)
}
```

For testing `errors.Join`-wrapped chains, walk the multi-error tree (see `examples/urlshort/internal/config/config_test.go::containsCode` for a reference helper).

## Limitations

- **No stack traces.** Causes are wrapped, not framed. Use `slog` with `Cause` (the wrapped error) if you need source info.
- **No retryability flag.** A `Kind` doesn't say "retry me". Decide at the call site based on Kind (e.g. `Unavailable`/`Timeout` → retry; `Validation` → don't).
- **No localisation.** `Message` is for humans-but-developers. Translate at the UI layer.
- **`Code` is your contract.** Never reuse a code for a different meaning between versions.

## See also

- [`errs/errsval`](errsval/README.md) — bridge from `go-playground/validator` to `*errs.Error{Kind: Validation}`
- [`fibermap`](../fibermap/README.md) — `ErrorHandler` wires `errs.HTTP` into Fiber
- [`db`](../db/README.md), [`auth`](../auth/README.md), [`clients/*`](../clients/) — every package returns `*errs.Error`
