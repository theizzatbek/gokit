# fibermap/bind

Generic-хелперы декодирования запроса — `Body[T]`, `Query[T]`, `Params[T]`, `Header[T]` — поверх Fiber-парсеров + опционального `Validator`. Используются напрямую через `fibermap.RegisterHandlerWithBody/Query/Params/Headers` для авто-декодирования + валидации перед вызовом вашего типизированного хендлера.

**Родитель:** [../README.md](../README.md)
**Импорт:** `github.com/theizzatbek/gokit/fibermap/bind`

## Использование

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
        return err   // *errs.Error{Kind: Validation} — маппится в 400 через fibermap.ErrorHandler
    }
    // body.Title декодирован + валидирован
}
```

`Validator` — это интерфейс (`Struct(any) error`) — передайте `nil`, чтобы пропустить валидацию (всё равно декодирует). Sibling-хелперы:

| Хелпер | Читает из | Тэг |
|---|---|---|
| `bind.Body[T](c, v)` | request body (JSON/form/multipart согласно Content-Type) | `json:"..."` |
| `bind.Query[T](c, v)` | URL query string | `query:"..."` |
| `bind.Params[T](c, v)` | path-параметры (`:name`) | `params:"..."` |
| `bind.Header[T](c, v)` | заголовки запроса | `header:"X-Name"` |

## Заметки

- **Валидатор опциональный.** Передайте `nil`, если вам нужно декодирование без валидации — полезно в тестах или для raw passthrough.
- **First-class `*errs.Error`.** И ошибки декодирования, и валидации возвращают типизированные ошибки с `KindValidation`, так что `fibermap.ErrorHandler` маппит их в 400 с body, заполненным `FieldError`.
- **Generic `T` — это ваша структура.** Определите по одной на запрос, держите тэги + правила валидации рядом с типом.
- **Обычно вы не вызываете их напрямую** — `fibermap.RegisterHandlerWithBody` и друзья оборачивают их, и `SetValidator(v)` на engine — каноническое место, где валидатор устанавливается один раз.
- **Кастомный маппинг ошибок** через `Engine.SetBindErrorHandler(fn)` — заменяет дефолтную форму `*errs.Error` на свою.

## См. также

- [`fibermap`](../README.md) — RegisterHandlerWithBody/Query/Params/Headers оборачивает эти хелперы
- [`errs`](../../errs/README.md) — форма `*errs.Error{Kind: Validation}`, возвращаемая при ошибке
- [`errs/errsval`](../../errs/errsval/README.md) — конвертирует `validator.ValidationErrors` в `*errs.Error.Details`
</content>
