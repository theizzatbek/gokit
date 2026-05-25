# errs/errsval

Bridge from [go-playground/validator/v10](https://github.com/go-playground/validator) `ValidationErrors` into `*errs.Error{Kind: Validation}` with one `FieldError` per failed rule populated. Kept in a subpackage so `errs/` itself stays stdlib-only.

**Parent:** [../README.md](../README.md)
**Import:** `github.com/theizzatbek/gokit/errs/errsval`

## Use

```go
import (
    "github.com/go-playground/validator/v10"
    "github.com/theizzatbek/gokit/errs/errsval"
)

v := validator.New(validator.WithRequiredStructEnabled())

type Req struct {
    Email string `json:"email" validate:"required,email"`
    Age   int    `json:"age"   validate:"required,min=18"`
}

func handler(c *fiber.Ctx) error {
    var r Req
    _ = c.BodyParser(&r)
    if err := v.Struct(r); err != nil {
        return errsval.FromValidator(err)
        // *errs.Error{Kind: Validation, Code: "invalid_body",
        //   Details: [{Field: "email", Rule: "required"}, …]}
        // → 400 via fibermap.ErrorHandler
    }
    // …
}
```

## Notes

- **`FromValidator(err)` is idempotent on already-typed errors.** If `err` isn't a `validator.ValidationErrors`, returns it unchanged — safe to wrap unconditionally.
- **Each `validator.FieldError` becomes one `errs.FieldError`** with `Field` = the struct field name (or json tag, depending on validator's `RegisterTagNameFunc`), `Rule` = the failed tag, `Param` = the tag param (e.g. `"18"` for `min=18`), `Message` = a generic human-readable message.
- **For nicer messages**, post-process the `Details`:
  ```go
  e := errsval.FromValidator(err).(*errs.Error)
  for i := range e.Details {
      e.Details[i].Message = nicer(e.Details[i])
  }
  ```
- **`fibermap.bind` already wires this** — you rarely call `FromValidator` directly. Use `bind.Body[T](c, v)` and the conversion happens in the bind layer.

## See also

- [`errs`](../README.md) — parent package
- [`fibermap/bind`](../../fibermap/bind/README.md) — auto-invokes `FromValidator` on bind failures
