# errs

Типизированные доменные ошибки + HTTP-маппинг. Stdlib-only, без fiber/validator.

## `errs/`

Типизированные доменные ошибки + HTTP-маппинг. `*errs.Error{Kind, Code, Message, Details, Cause}` carries a closed-enum `Kind`, a stable string `Code`, optional `Details` for field-level failures, and the wrapped `Cause`. Per-`Kind` constructors (`errs.NotFound`, `errs.Validation`, …) and `…f` Sprintf variants. `errs.Wrap(err, kind, code, msg)` lifts an underlying error. `errs.HTTP(err) (status, body)` produces the wire shape `{code, message, details?}`. `*Error` implements `slog.LogValuer` so `logger.Error("...", "err", e)` emits structured attrs. Stdlib-only — no Fiber, no validator deps.

## `errs/errsval/`

Converts `validator.ValidationErrors` into `*errs.Error` of `KindValidation` with populated `Details`. Depends on `go-playground/validator/v10`. Kept in a subpackage so `errs/` stays stdlib-only.