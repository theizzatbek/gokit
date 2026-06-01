# errs/errsval

Мост от [go-playground/validator/v10](https://github.com/go-playground/validator) `ValidationErrors` к `*errs.Error{Kind: Validation}` с одним `FieldError` на каждое сработавшее правило. Держится в подпакете, чтобы сам `errs/` оставался stdlib-only.

**Родитель:** [../README.md](../README.md)
**Импорт:** `github.com/theizzatbek/gokit/errs/errsval`

## Использование

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
        // → 400 через fibermap.ErrorHandler
    }
    // …
}
```

## Заметки

- **`FromValidator(err)` идемпотентен на уже типизированных ошибках.** Если `err` не `validator.ValidationErrors`, возвращает её без изменений — безопасно оборачивать безусловно.
- **Каждая `validator.FieldError` становится одной `errs.FieldError`** с `Field` = имя поля структуры (или json-тэг, в зависимости от `RegisterTagNameFunc` валидатора), `Rule` = сработавший тэг, `Param` = параметр тэга (например, `"18"` для `min=18`), `Message` = generic human-readable сообщение.
- **Для более красивых сообщений** обрабатывайте `Details` после:
  ```go
  e := errsval.FromValidator(err).(*errs.Error)
  for i := range e.Details {
      e.Details[i].Message = nicer(e.Details[i])
  }
  ```
- **`fibermap.bind` уже это подключает** — `FromValidator` напрямую вы вызываете редко. Используйте `bind.Body[T](c, v)`, и конвертация происходит в bind-слое.

## См. также

- [`errs`](../README.md) — родительский пакет
- [`fibermap/bind`](../../fibermap/bind/README.md) — авто-вызывает `FromValidator` при bind-ошибках
</content>
