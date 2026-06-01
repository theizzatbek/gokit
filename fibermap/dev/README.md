# fibermap/dev

Dev-only DX-tools для kit-based-сервисов: HTML error pages со
stack-trace'ами, route- / config-inspector'ы. Auto-disabled когда
`ENV != dev`.

**Импорт:** `github.com/theizzatbek/gokit/fibermap/dev`

## Quickstart

```go
import "github.com/theizzatbek/gokit/service"

svc, _ := service.New[App, Claims](ctx, cfg,
    service.WithDevMode(""),  // mounts /_dev/routes + /_dev/config
)
```

`WithDevMode` сам gate'ит mount по `Config.Service.Env == "dev"` —
безопасно в prod-deployment'е (option no-op'ится + warn-log
объясняет почему).

Для **non-service** Fiber-apps:

```go
import "github.com/theizzatbek/gokit/fibermap/dev"

app := fiber.New(fiber.Config{
    ErrorHandler: dev.ErrorHandler(nil),  // HTML errors на text/html
})
app.Get("/_dev/routes", dev.RoutesHandler(app))
app.Get("/_dev/config", dev.ConfigHandler(dev.WithExtraRedaction("INTERNAL")))
```

## Что есть

### HTML error pages

`dev.ErrorHandler(inner)` оборачивает default-error-handler. Content-negotiates:
- `Accept: text/html` → HTML-страница со stack-trace'ом + headers + request-details + error-code/kind/message
- остальное → fall-through на `inner` (`fibermap.ErrorHandler` default — JSON-shape)

Без kit'овского `*errs.Error` тоже работает: stdlib `error` → 500 + raw-message в HTML.

### `/_dev/routes` — route inspector

JSON по умолчанию, HTML на `Accept: text/html`. Sort'нутый список **всех** mount'ed routes (включая kit-wired'ые `/healthz`, `/metrics`, `/_dev/*`).

### `/_dev/config` — env inspector

Список **всех** env-vars с auto-redaction чувствительных (по умолчанию: `PASSWORD`/`SECRET`/`TOKEN`/`PRIVATE`/`KEY`/`DSN`/`DATABASE_URL`/`REDIS_URL`/`NATS_URL`). Custom-substring через `dev.WithExtraRedaction("INTERNAL", "BILLING")`.

JSON/HTML negotiate.

## Опции

| Опция | Заметки |
|---|---|
| `service.WithDevMode(prefix, opts...)` | Auto-wire всё. `prefix` default `"/_dev"`. Opts forwarded в ConfigHandler. |
| `dev.WithExtraRedaction(substrings...)` | Дополнительные substring-matches для маскировки env-var values'ов. Case-insensitive. |

## Безопасность

- **WithDevMode гейт'ит mount по `ENV == "dev"`**. Service.New при `ENV != "dev"` skip'ает mount + warn-log'ает чтобы dev'у было понятно почему inspector не работает на staging'е.
- **Redaction только для `KEY`-name-match'ей**. Значения в HTML-странице escape'ятся, но если operator зашиб значение в env-var с name'ом, который не match'ит redaction-list — оно отобразится. Add explicit substrings через `WithExtraRedaction`.
- **NEVER expose через public LB**. Inspectors показывают route-table + effective-env — это intel для attacker'а. Используйте отдельный internal-ingress / kubectl-port-forward для доступа.

## Ограничения

- **No JS** — no live-reload, no interactive table-filtering. Plain HTML + CSS-inline.
- **Stack-trace from `runtime.Stack`** — показывает Go-stack на момент error-handler call'а, не оригинальный site вызова (Fiber обернул middleware'ы между). Полезно для "где упало", не для "почему данные такие".
- **No request-body capture** — body уже consumed Fiber'ом к моменту error-page render'а. Inspect body через `LoggerInjector` отдельно.

## См. также

- [`service.WithDevMode`](../../service/README.md) — auto-wire через service.New
- [`fibermap.ErrorHandler`](../README.md) — production-shape error handler, который dev.ErrorHandler делегирует
- [`/preflight`](../../service/README.md) — boot-time precheck (отдельная фича, pairs с `kit doctor`)
</content>
