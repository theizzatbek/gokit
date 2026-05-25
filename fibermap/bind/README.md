# fibermap/bind

Generic request-decoding helpers — `Body[T]`, `Query[T]`, `Params[T]`, `Header[T]` — backed by Fiber's parsers + an optional `Validator`. Used directly by `fibermap.RegisterHandlerWithBody/Query/Params/Headers` to auto-decode + validate before invoking your typed handler.

**Parent:** [../README.md](../README.md)
**Import:** `github.com/theizzatbek/gokit/fibermap/bind`

## Use

```go
import (
    "github.com/gofiber/fiber/v2"
    "github.com/go-playground/validator/v10"
    "github.com/theizzatbek/gokit/fibermap/bind"
)

type CreateRequest struct {
    Title string `json:"title" validate:"required,min=1,max=200"`
}

func handler(c *fiber.Ctx) error {
    body, err := bind.Body[CreateRequest](c, validator.New())
    if err != nil {
        return err   // *errs.Error{Kind: Validation} — maps to 400 via fibermap.ErrorHandler
    }
    // body.Title is decoded + validated
}
```

`Validator` is an interface (`Struct(any) error`) — pass `nil` to skip validation (still decodes). Sibling helpers:

| Helper | Reads from | Tag |
|---|---|---|
| `bind.Body[T](c, v)` | request body (JSON/form/multipart per Content-Type) | `json:"..."` |
| `bind.Query[T](c, v)` | URL query string | `query:"..."` |
| `bind.Params[T](c, v)` | path parameters (`:name`) | `params:"..."` |
| `bind.Header[T](c, v)` | request headers | `header:"X-Name"` |

## Notes

- **Validator is opt-in.** Pass `nil` if you want decoding without validation rules — useful in tests or for raw passthrough.
- **First-class `*errs.Error`.** Both decode and validation failures return typed errors with `KindValidation` so `fibermap.ErrorHandler` maps them to 400 with a `FieldError`-populated body.
- **The generic `T` is your struct.** Define one per request, keep tags + validate rules colocated with the type.
- **Typically you don't call these directly** — `fibermap.RegisterHandlerWithBody` and friends wrap them and the engine's `SetValidator(v)` is the canonical place to set the validator once.
- **Custom error mapping** via `Engine.SetBindErrorHandler(fn)` — replace the default `*errs.Error` shape with your own.

## See also

- [`fibermap`](../README.md) — RegisterHandlerWithBody/Query/Params/Headers wrap these helpers
- [`errs`](../../errs/README.md) — the `*errs.Error{Kind: Validation}` shape returned on failure
- [`errs/errsval`](../../errs/errsval/README.md) — convert `validator.ValidationErrors` into `*errs.Error.Details`
