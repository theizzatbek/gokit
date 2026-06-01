# runbook

Runtime kill-switch primitive. Ops flip-нул flag в admin-UI →
сервис прочитал через `runbook.Enabled(...)` → feature/path/region
disabled — **без redeploy'а**. Production-incident-response tool.

**Импорт:** `github.com/theizzatbek/gokit/runbook`

## Quickstart

```go
import "github.com/theizzatbek/gokit/runbook"

rb, _ := runbook.New(runbook.NewMemoryStore(),
    runbook.WithAuditor(svc.Audit),      // every flip logged
    runbook.WithRefreshInterval(5*time.Second),  // multi-pod sync
)
defer rb.Close()

// In a handler:
if !rb.Enabled(ctx, "checkout") {
    return errs.Unavailable("checkout_paused", "feature paused by ops")
}

// In your admin Fiber sub-router (auth-gated):
adminGroup := app.Group("/_kit",
    svc.Auth.Bearer(auth.BearerRequired),
    svc.Auth.RequireRole("ops"),
)
runbook.Mount(adminGroup, "/runbook", rb,
    runbook.WithSubjectFn(func(c *fiber.Ctx) string {
        return auth.Subject[Claims](c)
    }),
)
```

## Семантика

- **Default-on.** Flag без stored-value → `Enabled == true`. Missing-config-blip **никогда** не disabled feature — ops should ACTIVELY disable.
- **Boolean-only.** Either enabled or disabled — no levels/percentages. Use a separate flag per granularity.
- **In-process cache.** `Enabled(...)` — O(1) read; не touch'ит Store на hot-path'е. Update'ы через `SetEnabled` или periodic refresh.
- **Multi-pod sync** через `WithRefreshInterval(d)` — каждый под подтягивает store-state каждые d sec'ов. Без него pod A flip'нул flag, pod B не узнает пока не restart'нется.

## Store-interface

```go
type Store interface {
    Get(ctx, name string) (enabled, found bool, err error)
    Set(ctx, name string, enabled bool) error
    All(ctx) (map[string]bool, error)
}
```

| Backend | When |
|---|---|
| `runbook.NewMemoryStore()` | Tests / single-pod-dev. Lost on restart. |
| Custom (Redis / Postgres) | Multi-pod-production. Persist across restarts. |

## Admin endpoints

`runbook.Mount(app, base, rb, opts...)` mount'ит:

| Endpoint | Заметки |
|---|---|
| `GET <base>` | HTML page — list всех flags, click-to-toggle buttons. |
| `GET <base>.json` | Snapshot cache как JSON. |
| `POST <base>/:flag` | Body `{"enabled": true/false}` — flips flag. |

**Wire auth/role middleware в front'е Mount'а.** Package не ship'ит auth — operators имеют different role-conventions.

## Audit-integration

`WithAuditor(audit.Logger)` подключает audit-trail: каждый `SetEnabled` emit'ит event:

```
Action:   "runbook.flag_changed"
Actor:    {Subject from WithSubjectFn, IP, UA from request}
Target:   {Type: "runbook_flag", ID: <flag-name>}
Outcome:  success
Metadata: {enabled: true/false}
```

## Use-cases

| Scenario | Flag pattern |
|---|---|
| Disable broken feature без deploy'а | `feature.<name>` → `false` |
| Drain traffic перед release-cut'ом | `accept.writes` → `false` |
| Disable region/replica | `region.eu-west-1` → `false` |
| Force-503 на проблемный route | check'ить в middleware: `if !rb.Enabled(ctx, "route." + c.Path()) { return 503 }` |
| Throttle expensive code-path | `feature.expensive_query` → `false` |

## Опции

| Опция | Заметки |
|---|---|
| `WithAuditor(audit.Logger)` | Audit-trail каждого Set. |
| `WithRefreshInterval(d)` | Periodic refresh из Store. Default 0 (no refresh). Multi-pod fleet — set this. |
| `WithLogger(fn)` | Optional `func(msg, kv...)` для one-line-per-Set log'ов. |

## API

| Method | Заметки |
|---|---|
| `New(store, opts...)` | Constructor. Loads initial cache synchronously. |
| `Enabled(ctx, name)` | O(1) read from cache. Default-on на miss. |
| `SetEnabled(ctx, name, enabled, actor)` | Persist + cache-update + audit-emit. |
| `All()` | Snapshot — для admin-UI. |
| `Close()` | Stop refresh-loop. Idempotent. |
| `Mount(app, base, rb)` | HTTP-handlers. |

## Ограничения

- **Boolean-only.** Для percentages → use external feature-flag-service (LaunchDarkly, Unleash, OpenFeature).
- **No multi-variant.** One name = one bool.
- **No targeting** (user-segments, regions без отдельного flag-per-region). Простой primitive — namespaced flags закрывают most ops-use-case'ы.
- **Cache eventual-consistency** под `WithRefreshInterval(d)` — propagation-delay до `d` секунд. Для emergency-kill'а — call SetEnabled через admin-endpoint на EACH pod.

## См. также

- [`audit`](../audit/README.md) — audit-trail для flag-changes
- [`auth`](../auth/README.md) — Bearer + RequireRole для admin-gate'а
</content>
