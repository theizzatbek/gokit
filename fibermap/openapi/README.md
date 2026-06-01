# fibermap/openapi

Генерация OpenAPI 3.0 spec'а + UI mount из `*fibermap.Engine[T]`. Рефлектит request/response модели, зарегистрированные через HandlerOptions `fibermap.WithBody`/`WithQuery`/`WithHeaders`/`WithParams`/`WithResponse`, обходит `Engine.Routes()`, эмитит JSON-спек и (через `Generator.Mount`) устанавливает `/openapi.json` + HTML-viewer на `/docs` (по умолчанию Scalar UI).

**Импорт:** `github.com/theizzatbek/gokit/fibermap/openapi`
**Зависит от:** `github.com/theizzatbek/gokit/fibermap`

> **Tip:** При использовании `gokit/service` вы можете объявить Info /
> Servers / SecuritySchemes / MiddlewareSecurity в top-level блоке
> `openapi:` в `routes.yaml` вместо передачи через Go-код. См.
> [service README](../../service/README.md#openapi-from-routesyaml).

## Зачем это нужно

Писать OpenAPI руками — это рутина: роуты и схемы дублируют код,
который уже есть в ваших хендлерах + routes.yaml. fibermap уже имеет
route-таблицу (`Engine.Routes()`) и typed-handler регистрационные
ручки (`WithBody`, `WithResponse`). `openapi.NewGenerator(eng).Mount()`
соединяет их и даёт вам `/openapi.json` + `/docs` без maintenance
burden'а — и `tags`/`summary`/`description` из вашего routes.yaml
проливаются прямо в.

## Quickstart

```go
import (
    "github.com/theizzatbek/gokit/fibermap"
    "github.com/theizzatbek/gokit/fibermap/openapi"
)

eng := fibermap.Default[AppCtx]()

// Регистрируйте handler'ы со схемами body/response, чтобы OpenAPI их отрефлектил.
fibermap.RegisterHandlerWithBody(eng, "tasks.create",
    func(c *fibermap.Context[AppCtx], req CreateRequest) error {
        return c.Status(201).JSON(Task{})
    },
    fibermap.WithResponse(201, Task{}),
    fibermap.WithResponse(400, errs.Response{}),
)

eng.LoadFile("routes.yaml")

// Один вызов для монтирования /openapi.json + /docs
gen := openapi.NewGenerator(eng,
    openapi.WithInfo(openapi.Info{
        Title:       "Tasks API",
        Version:     "0.1.0",
        Description: "Per-user task lists.",
    }),
    openapi.SecurityMapping("BearerAuth", openapi.HTTPBearer(), "bearer"),
)
if err := gen.Mount(); err != nil { return err }

eng.Run(fibermap.WithAddr(":3000"))
// → curl http://localhost:3000/openapi.json
// → open http://localhost:3000/docs    (Scalar UI)
```

## Публичный API

```go
type Generator[T any] struct{ /* unexported */ }

func NewGenerator[T any](eng *fibermap.Engine[T], opts ...Option) *Generator[T]

// Mount устанавливает /openapi.json + /docs как программные роуты на
// engine. Должен быть вызван ДО eng.Mount/Run.
func (g *Generator[T]) Mount(opts ...MountOpts) error

// Generate возвращает сырые JSON-байты — полезно для тулзов
// стиля `fibermap dump-openapi` или для записи спека в файл на build-time.
func (g *Generator[T]) Generate() ([]byte, error)
```

```go
type MountOpts struct {
    SpecPath   string  // default "/openapi.json"
    DocsPath   string  // default "/docs"
    Viewer     Viewer  // ScalarUI (default) | SwaggerUI | Redoc | NoViewer
    SpecURL    string  // только когда Viewer != NoViewer; default использует SpecPath
}
```

## Опции

| Опция | Заметки |
|---|---|
| `WithInfo(Info{Title, Version, Description, Contact})` | OpenAPI `info`-блок — установите per service |
| `WithServer(url, description)` | Добавляет запись в `servers[]`; вызывайте несколько раз для prod/staging |
| `WithSecurity(name, SecurityScheme)` | Определяет запись в `components.securitySchemes` (HTTPBearer/HTTPBasic/APIKey/OAuth2) |
| `MapMiddlewareToSecurity(middleware, schemeName)` | Говорит generator'у "роуты с этим middleware требуют эту security-схему" |
| `SecurityMapping(schemeName, scheme, middlewares...)` | Удобство: `WithSecurity` + `MapMiddlewareToSecurity` в одном вызове |
| `WithDefaultResponse(status int, model any)` | Добавляет default-response (например, 400/401/403/404/500 = errs.Response{}) в каждую операцию, которая не override'ит |

## Common patterns

### Подключение security-схем

```go
gen := openapi.NewGenerator(eng,
    openapi.SecurityMapping("BearerAuth", openapi.HTTPBearer("JWT"), "bearer"),
    openapi.SecurityMapping("BasicAuth",  openapi.HTTPBasic(),       "basic"),
    openapi.SecurityMapping("ApiKey",     openapi.APIKey("X-API-Key", "header"), "api_key"),
)
```

Роуты, чей middleware-список содержит `bearer`, автоматически получают `security: [{BearerAuth: []}]` в сгенерированном спеке.

### Default error responses для каждой операции

```go
gen := openapi.NewGenerator(eng,
    openapi.WithDefaultResponse(400, errs.Response{}),
    openapi.WithDefaultResponse(401, errs.Response{}),
    openapi.WithDefaultResponse(403, errs.Response{}),
    openapi.WithDefaultResponse(404, errs.Response{}),
    openapi.WithDefaultResponse(500, errs.Response{}),
)
```

Тогда в handler-регистрациях вы объявляете только success-response:

```go
fibermap.RegisterHandlerWithBody(eng, "tasks.create",
    func(c *fibermap.Context[AppCtx], req CreateRequest) error { /* … */ },
    fibermap.WithResponse(201, Task{}),  // только success — defaults заполняют остальное
)
```

### Выбор docs UI

```go
gen.Mount(openapi.MountOpts{Viewer: openapi.ScalarUI})  // default — modern, fast
gen.Mount(openapi.MountOpts{Viewer: openapi.SwaggerUI})
gen.Mount(openapi.MountOpts{Viewer: openapi.Redoc})
gen.Mount(openapi.MountOpts{Viewer: openapi.NoViewer})   // только /openapi.json
```

### Кастомные mount-пути

```go
gen.Mount(openapi.MountOpts{
    SpecPath: "/api/openapi.json",
    DocsPath: "/api/docs",
})
```

### Несколько окружений — `servers[]`

```go
openapi.NewGenerator(eng,
    openapi.WithServer("https://api.prod.example.com", "production"),
    openapi.WithServer("https://api.staging.example.com", "staging"),
    openapi.WithServer("http://localhost:3000", "local"),
)
```

### Build-time spec dump (CI integration)

```go
data, err := gen.Generate()
_ = os.WriteFile("openapi.json", data, 0644)
// Теперь diff'те против закоммиченного openapi.json в CI, чтобы поймать непреднамеренные API-изменения
```

## Что рефлектится

| Метаданные роута | Источник |
|---|---|
| `paths[<path>].<method>.summary` | YAML `summary:` |
| `paths[<path>].<method>.description` | YAML `description:` |
| `paths[<path>].<method>.tags` | YAML `tags:` (array) |
| `paths[<path>].<method>.operationId` | YAML `name:` |
| Схема request body | HandlerOption `fibermap.WithBody(StructType{})` |
| Query-параметры | `fibermap.WithQuery(StructType{})` — поля с тэгом `query:"name"` |
| Path-параметры | `fibermap.WithParams(StructType{})` + YAML-сегменты `:name` |
| Header-параметры | `fibermap.WithHeaders(StructType{})` — поля с тэгом `header:"X-Name"` |
| Response-схемы | `fibermap.WithResponse(status, StructType{})` per-status |
| Security-требование | `MapMiddlewareToSecurity`, соответствующий middleware-списку роута |
| Default-responses | `WithDefaultResponse(status, StructType{})` |

Schema-reflection использует Go struct-тэги: `json:"name"`, `validate:"required,min=1,max=200"`, `description:"..."`. Правила `validate` транслируются в OpenAPI `required` / `minLength` / `maximum` / `enum` и т.д.

## Ограничения

- **Schema reflection покрывает JSON-тэги + правила validator'а.** Кастомные JSON-маршаллеры / интерфейсы не рефлектятся.
- **Discriminated unions** требуют ручных `oneOf` schema-override'ов — не auto-derived.
- **Нет spec-версионирования** — `WithInfo.Version` — это OpenAPI doc-version, не API contract version. Бампайте на breaking changes руками.
- **Polymorphic responses** (разная схема per status из одного handler'а) поддержаны через несколько вызовов `WithResponse(status, model)`.
- **YAML route metadata — это source of truth.** `summary`/`description`/`tags`, установленные в struct-тэгах или godoc, НЕ рефлектятся — держите их в routes.yaml.
- **Scalar/Swagger/Redoc UI грузят свои CDN-ассеты.** Нет интернета на render-time → blank UI. JSON на `/openapi.json` всё ещё работает.

## См. также

- [`fibermap`](../README.md) — регистрирует хендлеры + метаданные, которые этот пакет рефлектит
- [`errs`](../../errs/README.md) — `errs.Response{}` для default error-response схем
- [`examples/tasks`](../../examples/tasks/) — использует openapi для `/openapi.json` + `/docs` (Scalar UI)
- [`examples/urlshort`](../../examples/urlshort/README.md) — минимальный openapi mount
</content>
