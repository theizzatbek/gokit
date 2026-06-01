# audit/auditadmin

Browser-UI для audit-log query'ев + JSON-export. Plain HTML +
inline CSS — no JS-framework, no external assets.

**Импорт:** `github.com/theizzatbek/gokit/audit/auditadmin`

## Quickstart

```go
import "github.com/theizzatbek/gokit/audit/auditadmin"

// Auth-gate it FIRST.
adminGroup := app.Group("/admin",
    svc.Auth.Bearer(auth.BearerRequired),
    svc.Auth.RequireRole("compliance"),
)
auditadmin.Mount(adminGroup, "/audit", svc.Audit)
```

Mounts:

| Endpoint | Description |
|---|---|
| `GET /admin/audit` | HTML page с filter-form + results-table + paging |
| `GET /admin/audit.json` | Same filter; returns Events as `application/json` для exports |
| `POST /admin/audit/...` | — нет (UI read-only) |

## Filter (query-string)

| Param | Заметки |
|---|---|
| `actor` | Exact-match на `Actor.Subject`. |
| `action` | Trailing `*`-wildcard (e.g. `user.*`). |
| `outcome` | `success` / `failure` / `denied`. |
| `target_type` / `target_id` | Filter на Target. |
| `from` / `to` | RFC3339-timestamps, inclusive. |
| `limit` | Default 50, max 500. |
| `offset` | Default 0, для paging'а. |

## Security

- **Audit-table содержит PII** (Metadata, request-context). **NEVER** expose через public LB.
- **Auth + role-check OBLIGATORY** перед Mount'ом. Package умышленно не ships свой auth — different services have different role conventions.
- **JSON-export endpoint** ставит `Content-Disposition: attachment` — curl/browser download'ят сразу.

## UI-screenshot semantics

- Filter-bar сверху, форма с GET-method (refresh-friendly, bookmarkable).
- Table с rows: `time | actor | action | target | outcome | metadata`.
- Pager-buttons внизу. "JSON export" link преserve'ит current filter.
- Outcome rendered как coloured-tag (success-green, failure-red, denied-amber).

## API

| Function | Заметки |
|---|---|
| `Mount(app fiber.Router, base string, logger *audit.Logger)` | Wires HTML + JSON handlers. base без trailing-slash'а. |
| `MountOnEngine(app *fiber.App, base, logger)` | Convenience-wrapper. Same semantics. |

## Ограничения

- **Filter ORDER BY OccurredAt ASC** (forced) — newest-events внизу. Может быть unexpected для admin'ов used to "newest-first" UI'ов. Stable-pagination требует ASC; reverse-on-display — TBD.
- **No CSV/Excel-export** — only JSON. Compliance-tools обычно умеют JSON; CSV — отдельная задача.
- **No real-time-stream** — page показывает snapshot на момент request'а. Browser-refresh = new query.

## См. также

- [`audit`](../README.md) — core
- [`audit/auditmw`](../auditmw/README.md) — auto-emit middleware
- [`audit/auditpg`](../auditpg/README.md) — Postgres-store
</content>
