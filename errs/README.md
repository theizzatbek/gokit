# errs

Типизированные доменные ошибки с маппингом в HTTP. Один тип `*Error` несёт closed-enum `Kind`, стабильный строковый `Code`, опциональный `Details` для field-level ошибок и обёрнутый `Cause`. `errs.HTTP(err)` маппит в wire-форму, на которую согласен каждый пакет кита.

**Импорт:** `github.com/theizzatbek/gokit/errs`
**Зависит от:** только stdlib

## Зачем это нужно

Каждый сервис в итоге изобретает один и тот же словарь: "not found", "validation failed", "unauthorized", "internal". `errs` — это словарь плюс HTTP-маппинг таблица, так что `return err` из хендлера производит правильный статус + JSON body, без того чтобы каждый хендлер заново это решал. Любой другой пакет gokit возвращает `*errs.Error` для известных условий — поэтому контракт единый для `db`, `auth`, `clients/*`, `fibermap`.

## Quickstart

```go
import (
    "errors"
    xerrs "github.com/theizzatbek/gokit/errs"
)

// Конструирование
if user == nil {
    return xerrs.NotFound("user_not_found", "user does not exist")
}
if err := svc.charge(); err != nil {
    return xerrs.Wrap(err, xerrs.KindUnavailable, "payment_provider_down", "stripe call failed")
}

// Потребление (например, в fibermap'овском ErrorHandler — уже подключено через fibermap.ErrorHandler)
status, body := xerrs.HTTP(err)  // status=404, body={code:"user_not_found", ...}
return c.Status(status).JSON(body)
```

## Таксономия `Kind`

| Kind | HTTP статус | Когда использовать |
|---|---|---|
| `KindNotFound` | 404 | Ресурс не существует |
| `KindAlreadyExists` | 409 | Коллизия по уникальному ключу при create |
| `KindConflict` | 409 | Конфликт записи (конкурентное редактирование, нарушение fk) |
| `KindValidation` | 400 | Плохой ввод — неверная форма, неудачные правила валидации |
| `KindUnauthorized` | 401 | Нет / невалидные credentials |
| `KindPermission` | 403 | Аутентифицирован, но не разрешено |
| `KindRateLimited` | 429 | Слишком много запросов |
| `KindUnavailable` | 503 | Зависимость лежит (DB, upstream) |
| `KindTimeout` | 504 | Операция превысила deadline |
| `KindInternal` | 500 | Ошибка программиста / нераспознанный сбой |
| `KindUnknown` (zero value) | 500 | Не конструируйте напрямую; означает "не классифицирована" |

`HTTP(err)` возвращает 500 для любой `error`, которая не `*errs.Error` — так что необработанные ошибки fail safe, а не утекают внутрь.

## Конструкторы

У каждого `Kind` две версии:

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

Обернуть существующую `error`:

```go
errs.Wrap(err, errs.KindInternal, "db_failure", "database operation failed")
errs.Wrapf(err, errs.KindUnavailable, "stripe_call_failed", "stripe %s call failed", op)
```

`errors.Unwrap`, `errors.Is`, `errors.As` работают на обёрнутых ошибках.

## Common patterns

### Соглашение по именованию Code

Стабильное, machine-readable, lowercase snake_case. Per-package или per-domain префикс избегает коллизий:

```
user_not_found          // generic
auth_invalid_credentials
db_failure
apimap_github_get_user_not_found  // generated per-endpoint by clients/apimap
```

Code'ы — это публичный API для downstream-consumers (они могут switch'иться по ним в тестах или алертах). Относитесь к изменению Code как к изменению API.

### `FieldError` details для валидации

```go
errs.Validation("invalid_body", "request body failed validation",
    errs.FieldError{Field: "email", Rule: "required", Message: "email is required"},
    errs.FieldError{Field: "age", Rule: "min", Param: "18", Message: "must be at least 18"},
)
```

Wire-форма:

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

Для автоматической конвертации `go-playground/validator` `ValidationErrors` используйте [`errs/errsval`](errsval/README.md).

### Прикрепление details после конструирования

```go
e := errs.Validation("invalid_body", "bad request").
    WithDetails(
        errs.FieldError{Field: "x", Rule: "required"},
    )
```

### Инспектирование ошибок

```go
var e *xerrs.Error
if errors.As(err, &e) {
    switch e.Kind {
    case xerrs.KindNotFound:   // …
    case xerrs.KindValidation: // …
    }
    log.Info("known failure", "code", e.Code)
}

// или по Kind:
if errors.Is(err, somethingSentinel) { /* … */ }

// размотать до оригинальной причины:
var pgErr *pgconn.PgError
if errors.As(err, &pgErr) { /* … */ }
```

### Распаковка агрегатов `errors.Join`

Build/validate шаги кита возвращают `errors.Join(...)`, когда несколько
сбоев `*xerrs.Error` происходят одновременно. `errs.All` обходит join-дерево
(плюс стандартные цепочки `Unwrap`) и возвращает каждый `*Error`, до которого
может дотянуться, в depth-first порядке:

```go
if err := eng.Mount(app); err != nil {
    for _, e := range xerrs.All(err) {
        log.Warn("mount issue", "code", e.Code, "kind", e.Kind, "msg", e.Message)
    }
    return err
}
```

Wrap-цепочки (`Wrap(rootErr, ...)`) показывают оба уровня. Не-`*Error` члены
Join'а молча пропускаются; `errs.All(nil)` возвращает `nil`.

### Структурированное логирование

`*Error` реализует `slog.LogValuer`, так что передача его в `slog` автоматически эмитит структурированные поля:

```go
logger.Error("create user failed", "err", err)
// → {"level":"ERROR","msg":"create user failed",
//    "err":{"kind":"validation","code":"user_exists","message":"…"}}
```

## HTTP интеграция

`errs.HTTP(err) (int, Response)` — это та единственная функция, которую вызывает fibermap'овский `ErrorHandler`. Вы почти никогда её не вызываете напрямую — зарегистрируйте `fibermap.ErrorHandler(logger)` как ваш `fiber.Config.ErrorHandler` и делайте `return err` из хендлеров.

Для не-fibermap серверов (например, stdlib `net/http`):

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

## Тестирование

Точно матчите Kind + Code:

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

Для тестирования цепочек, обёрнутых через `errors.Join`, обходите multi-error дерево (см. `examples/urlshort/internal/config/config_test.go::containsCode` как референсный хелпер).

## Ограничения

- **Нет стектрейсов.** Causes оборачиваются, а не фреймятся. Используйте `slog` с `Cause` (обёрнутая ошибка), если нужна source info.
- **Нет флага retryability.** `Kind` не говорит "retry me". Решайте на call-site по Kind'у (например, `Unavailable`/`Timeout` → retry; `Validation` → нет).
- **Нет локализации.** `Message` — для humans-but-developers. Переводите на UI-слое.
- **`Code` — это ваш контракт.** Никогда не переиспользуйте код для другого смысла между версиями.

## См. также

- [`errs/errsval`](errsval/README.md) — мост от `go-playground/validator` к `*errs.Error{Kind: Validation}`
- [`fibermap`](../fibermap/README.md) — `ErrorHandler` подключает `errs.HTTP` к Fiber
- [`db`](../db/README.md), [`auth`](../auth/README.md), [`clients/*`](../clients/) — каждый пакет возвращает `*errs.Error`
</content>
