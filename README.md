# gokit

Композируемый Go service kit. Девять независимо импортируемых пакетов, которые
покрывают то, что каждый HTTP API сервис делает руками: роутинг, ошибки, базу
данных, авторизацию, исходящий HTTP, декларативные исходящие API, NATS event
streaming, декларативные NATS-подписчики и публишеры.

Каждый пакет можно использовать отдельно. Вместе они уводят вас от `main.go`
к сервису production-shape.

**Статус:** 0.x — API нестабильно.

## Пакеты

| Путь | Что делает |
|------|---|
| `fibermap/` | YAML-декларативный роутер для Fiber v2. Хендлеры и middleware по именам. Типизированный per-request контекст. Генерация OpenAPI. |
| `errs/` | Типизированные доменные ошибки (`Kind`, `Code`, `Details`, `Cause`) с маппингом в HTTP. Только stdlib. |
| `db/` | Обёртка над pgx pool. Транзакции с savepoint'ами. Healthcheck. Маппинг ошибок в `*errs.Error`. |
| `db/sqb/` | Опциональная обёртка над squirrel, преднастроенная на `$N` placeholders. |
| `auth/` | Выпуск / верификация JWT (EdDSA/ES256). Хеширование паролей через Argon2id. Ротация refresh-токенов. Готовая к монтированию Fiber middleware. |
| `clients/httpc/` | Конструктор исходящего `*http.Client`. Retry, per-attempt timeout, slog + Prometheus observability. |
| `clients/apimap/` | Декларативные исходящие вызовы: описание upstream API в YAML, вызов по имени. Auth и `${ENV_VAR}` secrets прямо в YAML. |
| `clients/nats/` | Типизированная обёртка над JetStream. Generic `Publisher[T]` / `Subscribe[T]`. Auto-ack handler model. |
| `clients/natsmap/` | Декларативные NATS-подписчики и публишеры через YAML. Типизированные хендлеры и публишеры по имени, `*Runtime.Drain()` для graceful shutdown. |
| `service/` | Опциональный all-in-one хелпер. `service.New(ctx, cfg)` собирает все остальные подпакеты в `Service[T, C]` runtime с auto-detect optionality, авто-смонтированными auth-хендлерами и Bearer-optional слоем. Сжимает `main.go` для типичного сервиса с ~270 строк до ~80. |

## Правила зависимостей

```
errs                      → только stdlib
db, db/sqb                → errs + pgx
clients/httpc             → errs + prometheus
clients/apimap            → errs + clients/httpc + yaml.v3
clients/nats              → errs + nats.go + prometheus
clients/natsmap           → errs + clients/nats + yaml.v3
auth                      → errs + crypto + jwt + fiber
fibermap                  → errs + fiber (только router-смежные подпакеты)
```

Корневой пакет `gokit` пустой — без экспортируемых символов. Импорт одного
подпакета не тянет остальные.

## Установка

```bash
go get github.com/theizzatbek/gokit/fibermap
go get github.com/theizzatbek/gokit/errs
go get github.com/theizzatbek/gokit/db
go get github.com/theizzatbek/gokit/auth
go get github.com/theizzatbek/gokit/clients/httpc
go get github.com/theizzatbek/gokit/clients/apimap
go get github.com/theizzatbek/gokit/clients/nats
go get github.com/theizzatbek/gokit/clients/natsmap

# опционально: автономный CLI для linting'а routes.yaml и экспорта schema
go install github.com/theizzatbek/gokit/cmd/fibermap@latest
```

Требуется Go 1.23+ и (для `fibermap/`) Fiber v2.

## Quickstart — fibermap роутер

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

## Поддержка редактора для `routes.yaml`

Добавьте эту строку в начало вашего `routes.yaml`:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/theizzatbek/gokit/main/fibermap/schema/routes.schema.json
```

VS Code (с [redhat.vscode-yaml]), GoLand и Vim с `coc-yaml` дают
автодополнение для `method` / `middleware_sets` / и т.д., hover-документацию
и inline-диагностику — опечатки в `middleware:` подсвечиваются до того,
как вы вообще запустите `go test`.

[redhat.vscode-yaml]: https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml

## CLI

```bash
fibermap validate routes.yaml    # schema-lint; ненулевой exit при проблемах
fibermap dump-schema             # печатает встроенную JSON Schema
```

`validate` запускает проверки уровня схемы (обязательные поля, валидные HTTP
методы, циклы в middleware_set, форма middleware). Он НЕ проверяет, что
имена handler / middleware / factory зарегистрированы — ваш Go-бинарь это
единственное место, где они живут. Для полной валидации (включая
регистрации) вызовите `Engine.Validate()` в Go-тесте или boot-скрипте.

## Примеры

- `examples/quickstart/` — минимальный Hello-world
- `examples/auth/` — JWT login + Bearer middleware
- `examples/nats/` — типизированный publisher / subscriber
- `examples/tasks/` — более полный сервис (config, db, auth, OpenAPI)
</content>
