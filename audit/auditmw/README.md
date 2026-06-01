# audit/auditmw

Fiber-middleware который auto-emit'ит `audit.Event`'ы из каждого
inbound-request'а. Closes wiring-gap для уже-shipped `audit`-пакета.

**Импорт:** `github.com/theizzatbek/gokit/audit/auditmw`

## Quickstart

```go
import "github.com/theizzatbek/gokit/audit/auditmw"

app.Use(auditmw.Middleware(svc.Audit,
    auditmw.WithSubject(func(c *fiber.Ctx) string {
        if p, ok := auth.From[MyClaims](c); ok {
            return p.Subject
        }
        return ""
    }),
    auditmw.WithSkipPaths("/healthz", "/readyz", "/metrics", "/preflight"),
))
```

## Что emit'ится автоматически

| Поле | Default-источник | Override |
|---|---|---|
| `Actor.Subject` | `WithSubject(fn)` | — required для non-anonymous-events |
| `Actor.IP` / `Actor.UA` | `c.IP()` / `User-Agent` | — |
| `Action` | `${METHOD}.${route-pattern}` (e.g. `POST./tasks/:id`) | `WithAction(fn)` |
| `Target.Type` | первый path-segment (e.g. `/tasks/:id` → `tasks`) | `WithTarget(fn)` |
| `Target.ID` | `c.Params("id")` или последний path-param | `WithTarget(fn)` |
| `Outcome` | `2xx → success`, `401/403 → denied`, остальное → `failure` | — |
| `Metadata.status` | response status-code | `WithMetadata(fn)` для extra-field'ов |

## Default-policy

| Aspect | Default |
|---|---|
| Methods | POST / PUT / PATCH / DELETE (mutating only) |
| Skip-paths | empty — добавляй через `WithSkipPaths` |
| Subject | empty (anonymous) пока не подключишь `WithSubject` |

Чтобы log'ить reads тоже — `WithIncludeMethods("GET", "POST", ...)`.

## Опции

| Опция | Заметки |
|---|---|
| `WithIncludeMethods(verbs...)` | Replace default verb-set. Pass uppercase. |
| `WithSkipPaths(paths...)` | Append exact-match-paths для skip'а. |
| `WithSubject(fn)` | Extract Actor.Subject (pull из auth.From). |
| `WithAction(fn)` | Override Action-verb. |
| `WithTarget(fn)` | Override Target. |
| `WithMetadata(fn)` | Stamp extra-fields на каждый event. |

## Fail-soft

Audit-store failures **никогда** не propagate'ятся как 500 client'у:
audit-blip can't turn a successful write into failed response. Errors
log'аются (`audit.Logger.cfg.Logger`) — silent для callers, visible для
ops.

## Wiring-pattern с `service.Service`

```go
svc, _ := service.New[App, Claims](ctx, cfg, ...)

// Build audit logger after service.New (needs svc.DB).
store := auditpg.New(svc.DB)
_ = auditpg.ApplySchema(ctx, svc.DB)
auditLogger, _ := audit.New(store, audit.Config{
    ServiceName: cfg.Service.NodeName,
    Logger:      svc.Logger(),
})

// Wire middleware via WithFiberMiddleware.
svc, _ = service.New[App, Claims](ctx, cfg,
    service.WithFiberMiddleware(auditmw.Middleware(auditLogger,
        auditmw.WithSubject(func(c *fiber.Ctx) string {
            return auth.Subject[Claims](c)
        }),
        auditmw.WithSkipPaths("/healthz", "/readyz", "/metrics", "/preflight"),
    )),
)
```

## См. также

- [`audit`](../README.md) — core audit-логгер + Store interface
- [`audit/auditadmin`](../auditadmin/README.md) — browser-UI для query/export
- [`audit/auditpg`](../auditpg/README.md) — Postgres-store
</content>
