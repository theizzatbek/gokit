# tasks — реалистичный пример fibermap

Небольшое per-user tasks/CRUD API, задуманное как **стартовый шаблон**,
а не teaching-демка. Скопируйте директорию, переименуйте, подкрутите,
shipните.

## Что тут реально лежит (и почему)

| Путь | Зачем |
| --- | --- |
| `main.go` | wire-up через `eng.Run(...)` — один вызов покрывает `fiber.New(custom config)`, `app.Use(request_id, auth.Bearer)`, `LoadFS` embedded `routes.yaml`, `Mount`, `Listen(":3000")` и graceful shutdown на SIGINT/SIGTERM |
| `routes.yaml` | декларативное дерево роутов, монтируемое через `Engine.LoadFS`; modeline сверху даёт editor-автодополнение через JSON Schema |
| `internal/appctx/` | struct `AppCtx` (user_id, role, request_id, scoped logger) + алиасы `Ctx` / `H` / `MW`, чтобы подписи хендлеров не несли generic-параметр |
| `internal/config/` | env-driven `Config` struct через `caarlos0/env/v11` — `ADDR`, `LOG_LEVEL`, `CORS_ORIGINS` и т.д. См. `.env.example` |
| `internal/auth/` | Fiber-level Bearer-token middleware (запускается **до** `ContextBuilder`) + fibermap-factory `require_role` |
| `internal/tasks/` | domain — модель `Task`, интерфейс `Store` (memory-impl за ним; поменяйте на postgres без касания хендлеров), хендлеры использующие `bind.Body[T]` с тэгами `go-playground/validator` |
| `internal/admin/` | эндпоинт `/admin/routes`, построенный на `Engine.Routes()` — удобный ops-эндпоинт, заодно показывает JSON-тэги на `RouteInfo` в действии |
| `main_test.go` | `fibermaptest.AssertRoute` для "routes.yaml говорит то, что мы думаем" + end-to-end запросы через `fiber.App.Test()` для "вся стопка реально отвечает" |

## Попробовать

```bash
go run ./examples/tasks
```

Demo-токены забиты в код (`internal/auth/auth.go`):
- `alice-token`, `bob-token` — `role=user`
- `root-token` — `role=admin`

```bash
# неаутентифицированно → 401
curl -i http://localhost:3000/api/v1/tasks

# alice создаёт задачу → 201
curl -i -H "Authorization: Bearer alice-token" \
        -H "Content-Type: application/json" \
        -d '{"title":"buy milk"}' \
        http://localhost:3000/api/v1/tasks

# alice листит свои задачи → 200 + JSON
curl -i -H "Authorization: Bearer alice-token" \
        http://localhost:3000/api/v1/tasks

# alice пробует удалить → 403 (require_role: [admin])
curl -i -X DELETE -H "Authorization: Bearer alice-token" \
        http://localhost:3000/api/v1/tasks/$TASK_ID

# root (admin) удаляет → 204
curl -i -X DELETE -H "Authorization: Bearer root-token" \
        http://localhost:3000/api/v1/tasks/$TASK_ID

# admin листит каждый роут, зарегистрированный fibermap → 200 + JSON
curl -i -H "Authorization: Bearer root-token" \
        http://localhost:3000/api/v1/admin/routes
```

## Конфигурация

Всё env-driven через `internal/config`. Зашитые defaults соответствуют
тому, что вы видите в `curl`-примерах выше — слушает `:3000`,
JSON-логи на `info`, открытый CORS, 100 req/min на IP. Override любого
поля через установку соответствующей env-переменной (или положив `.env`
рядом с бинарём и source'ом его).

| Var | По умолчанию | Смысл |
| --- | --- | --- |
| `ADDR` | _(unset)_ → `$PORT` / `:3000` | Listen-адрес. Когда не установлен, `fibermap.Run` чтит `$PORT` (cloud-конвенция) и фоллбэчит на `:3000`. Установите `ADDR` явно, чтобы переопределить оба |
| `SHUTDOWN_TIMEOUT` | `10s` | Бюджет graceful-drain на SIGINT/SIGTERM |
| `BODY_LIMIT` | `1048576` (1 MiB) | `fiber.Config.BodyLimit` |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `LOG_FORMAT` | `json` | `json` / `text` |
| `CORS_ORIGINS` | `*` | Comma-separated origins для `cors.AllowOrigins` |
| `CORS_METHODS` | `GET,POST,PATCH,DELETE,OPTIONS` | `cors.AllowMethods` |
| `RATE_LIMIT_MAX` | `100` | Запросов на окно на IP |
| `RATE_LIMIT_EXPIRATION` | `1m` | Длина окна |
| `ENV` | `development` | `development` / `staging` / `production` |
| `API_BASE_URL` | _(unset)_ | Если установлен, используется как `servers[0].url` в OpenAPI |

Полный template: [`.env.example`](.env.example).

## Паттерны, которые стоит скопировать

1. **`AppCtx` несёт всё request-scoped.** `UserID`, `Role`,
   `RequestID` и `*slog.Logger`, pre-bound с обоими. Хендлеры делают
   `c.Data.Log.Info("created", ...)`, и строки автоматически
   коррелируют с пользователем и запросом.

2. **Auth на уровне Fiber, авторизация на уровне fibermap.**
   `Bearer()` запускается через `app.Use(...)` *до* fibermap-овского
   `ContextBuilder`, чтобы он мог установить locals, которые builder
   читает. `require_role` запускается как fibermap chain entry, так
   что виден в `routes.yaml` и легко introspect'ируется/тестируется.

3. **`embed.FS` для `routes.yaml`.** `//go:embed routes.yaml` плюс
   `fibermap.WithRoutesFS(routesFS)` → один бинарь, никаких
   working-directory ловушек в деплое.

4. **`Store` — это интерфейс.** In-memory impl нормален для демо;
   замена на Postgres / SQLite / DynamoDB требует только реализации
   тех же пяти методов. Хендлеры не меняются.

5. **`fibermaptest` для "YAML говорит то, что мы думаем".**
   `main_test.go` ассертит количество роутов, имена хендлеров, цепочки
   middleware и тэги напрямую против загруженного `routes.yaml` — так
   что merge, который случайно убирает `require_role: [admin]` с
   DELETE, валит CI немедленно.

6. **`/admin/routes` для ops.** Крошечный эндпоинт, большой leverage —
   on-call может `curl ../admin/routes` и видеть live-таблицу роутов
   без перечитывания конфига.

7. **`bind.Body[T]` + тэги `validator:` для request-body.** Хендлеры
   объявляют request-struct с тэгами `validate:`
   (`required`, `min`, `max`, `omitempty`, ...) и хендлер — это
   однострочник `req, err := bind.Body[createReq](c.Ctx, h.Validator)`.
   Cross-field правила, которые не влезают в тэги ("at least one of
   title, done"), остаются как hand-rolled проверки после успеха
   `bind.Body`.

8. **Built-in response cache с per-user KeyBy.** Read-only роуты
   (`GET /tasks`, `GET /tasks/:id`) объявляют `cache:` прямо в YAML —
   скаляр (`cache: 10s`) для просто TTL или mapping для
   `control`/`headers`/`vary_header`. `main.go` подключает
   engine-wide defaults через `eng.SetCacheDefaults(fibermap.CacheDefaults[AppCtx]{
   KeyBy: c.Data.UserID })`, так что cache-namespace per-user —
   список alice никогда не отдаётся bob'у. По умолчанию storage —
   in-process map Fiber'а (нормально для single instance); поменяйте
   на `Storage: redis.New(...)` из
   [`gofiber/storage`](https://github.com/gofiber/storage) для
   shared cache между репликами.

9. **`eng.Run(...)` вместо hand-rolled lifecycle.** `main.go` использует
   one-call launcher: `WithFiberConfig` подключает кастомный
   `ErrorHandler`, `WithUse(fibermap.RequestID(), auth.Bearer())`
   устанавливает два Fiber-level middleware (встроенный `RequestID`
   заменяет hand-rolled 8-line копию), `WithRoutesFS(routesFS)`
   грузит embedded YAML. SIGINT/SIGTERM с 10s drain'ом — дефолт —
   никакого ручного boilerplate'а `signal.NotifyContext` / `ShutdownWithContext`.

10. **Security-middleware через `WithUse` в фиксированном порядке.** `helmet` →
    `cors` → `limiter` → `auth`. Порядок в `main.go` с rationale
    прописанным в комментарии: helmet декорирует каждый ответ, cors
    должен идти до auth, чтобы OPTIONS-preflight пробился, limiter
    должен идти до auth, чтобы anonymous-flood не платил за
    credential-lookup, auth последний, чтобы locals были populated
    для `ContextBuilder`.

11. **Basic-auth пароли bcrypt-хешированы.** `internal/auth/auth.go`
    хранит `{user → bcryptHash}` и верифицирует через
    `bcrypt.CompareHashAndPassword`. Каждая ветка failure'а —
    неизвестный user, неверный пароль, malformed header — возвращает
    одно идентичное 401 body, чтобы демо не утекало, существует ли
    username. Demo-логины всё ещё работают: `alice:secret`,
    `bob:secret`, `root:admin`.

## Что добавить для реального production

Большинство скучных вещей уже подключены в этом примере (env-конфиг,
body-limit, helmet, CORS, per-IP rate-limit, типизированные error-responses,
graceful shutdown, метрики). Что всё ещё demo-only:

- Замените `tasks.NewMemStore()` на database-backed `Store`
  (Postgres / SQLite / DynamoDB — хендлеры не изменятся, только
  store-impl).
- Замените `demoTokens` / `demoBasic` на реальный verifier — JWT-библиотека,
  бьющая в JWKS вашего IdP, opaque-token Redis-lookup или что у вас
  за auth-модель.
</content>
