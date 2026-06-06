# auth/sessions

Server-side cookie sessions поверх JWT-first auth. Use it когда browser-rendered apps нужны:

- **Server-side revocation** — admin кликнул "log out everywhere" → каждая active session кончается в одном round-trip'е. JWT'ы этого не умеют без blocklist'а.
- **Sliding inactivity timeout** — sessions extend на каждом hit'е, expire после IdleTimeout молчания.
- **First-party cookie-auth** — никакого Authorization-header'а в HTML-form'ах / `fetch + credentials: 'include'`.

Sessions coexist с JWT'ом: services могут mount'ить и Bearer, и Session middleware на engine; первая, заполнившая Locals-principal — winner. Login-flows выбирают что issue'ить: API'и → JWT, web-flows → session cookie.

**Импорт:** `github.com/theizzatbek/gokit/auth/sessions` + `auth/sessionsredis` (default Redis backend)

## Quickstart

```go
import (
    "github.com/theizzatbek/gokit/auth/sessions"
    "github.com/theizzatbek/gokit/auth/sessionsredis"
)

store := sessionsredis.New(svc.Redis.Redis(), "app:")
sm, _ := svc.Auth.Sessions(sessions.Config{
    Store:       store,
    TTL:         24 * time.Hour,
    IdleTimeout: time.Hour,
    SameSite:    "Lax",
})

// Login route — issue session.
app.Post("/login", func(c *fiber.Ctx) error {
    // … verify credentials …
    return sm.Issue(c, "u-42", MyClaims{Plan: "pro"}, []string{"read"}, nil)
})

// Logout.
app.Post("/logout", sm.Logout)

// Protect routes — same *Principal[C] surface как auth.Bearer.
app.Use(sm.Middleware(sessions.Required))
app.Get("/me", func(c *fiber.Ctx) error {
    p, _ := auth.From[MyClaims](c)
    return c.JSON(p)
})

// Admin tool — log out everywhere.
func revoke(ctx context.Context, subject string) error {
    return sm.LogoutEverywhere(ctx, subject)
}
```

## Cookie defaults (secure-first)

| Атрибут | Default | Зачем |
|---|---|---|
| `HttpOnly` | true (always) | JS не должен видеть session-id. |
| `Secure` | true | HTTPS-only. Flip через `InsecureCookie=true` ТОЛЬКО для local-dev'а. |
| `SameSite` | `Lax` | Стандартный compromise — CSRF-resistant но не ломает navigation-from-link. |
| `Path` | `/` | Cookie scope. |

## SessionStore-контракт

```go
type SessionStore interface {
    Create(ctx, sess *Session) error
    Get(ctx, id string) (*Session, error)       // (nil, nil) на miss
    Touch(ctx, id, lastSeen, expires) error     // sliding refresh
    Delete(ctx, id) error
    DeleteForSubject(ctx, subject) error        // "log out everywhere"
}
```

Default-backend — `auth/sessionsredis` (HASH per session + SET per subject для O(N) bulk-delete где N = sessions per user, не whole keyspace).

`sessions.NewMemoryStore()` — in-process для тестов / single-pod dev'а. **НЕ для prod'а** — restart wipes everything, никакого GC.

## Principal[C] integration

Manager rebuilds `*Principal[C]` из сохранённой Session и stuff'ит в тот же Locals-slot, что и Bearer. Это значит:

- `auth.From[C](c)` / `auth.Subject[C](c)` работают transparently
- `auth.RequireScope("read")` middleware работает transparently
- `auth.RequireRole("admin")` middleware работает transparently

Session-side claims хранятся как JSON в `Session.Claims` (`json.RawMessage`), декодятся в C при Middleware-load'е. Это значит:

- Store stays C-agnostic (один store-impl для разных Auth-инстансов).
- Schema-drift между deploy'ями (изменили C, забыли мигрировать) → middleware force-logout'ит клиента и обнуляет cookie вместо 500.

## Modes

| Mode | Поведение |
|---|---|
| `Optional` | Cookie present + valid → populate Principal. Otherwise → passthrough (анонимная route). |
| `Required` | Cookie missing/invalid/expired → 401 `sessions_missing`. |

## Sliding refresh

Каждый успешный Middleware-hit:
1. `now = time.Now()`
2. `newExp = min(now + IdleTimeout, CreatedAt + TTL)` — sliding cap'нутый на абсолютный TTL.
3. Если `newExp > currentExp` → `store.Touch(id, now, newExp)` + cookie re-set с новым `Expires`.

Это даёт active users поведение "не выкидывает", а inactive — graceful timeout без явного logout'а.

## Error-mapping

| Случай | `*errs.Error` |
|---|---|
| Required mode + no/expired cookie | 401 `sessions_missing` |
| Tampered cookie (shape-check fails) | 401 `sessions_invalid_id` |
| Store transport error | 503 `sessions_store_failed` |
| Schema-drift на decode'е C | 401 `sessions_claims_decode` (forced re-login) |
| Missing Store / TTL | `sessions_invalid_config` |

## API-поверхность

| Метод | Заметки |
|---|---|
| `Auth.Sessions(cfg, opts...)` | Construct Manager bound к *Auth[C]. Options — observability + lifecycle hooks. |
| `(sm) Issue(c, subject, claims, scopes, roles)` | Create session + set cookie. Fires `OnIssue`. |
| `(sm) Logout(c)` | Delete session + clear cookie. Fires `OnLogout`. |
| `(sm) LogoutEverywhere(ctx, subject)` | Bulk delete все sessions subject'а. Fires `OnLogoutEverywhere` with revoked count (когда Store реализует Lister, иначе −1). |
| `(sm) RevokeByID(ctx, id)` | Admin "force-logout this specific session" — без оглядки на cookie. Идемпотентен; пустой id short-circuit. Fires `OnLogout`. |
| `(sm) Middleware(mode)` | Fiber middleware с Optional/Required. Fires `OnExpire` при in-flight удалении истёкшей сессии. |

## Observability + lifecycle хуки

`a.Sessions(cfg, opts...)` принимает trailing-options (back-compat с `a.Sessions(cfg)`):

- `WithMetrics(reg prometheus.Registerer)` регистрирует:
  - `sessions_ops_total{op,outcome}` — op: `issue|logout|logout_all|revoke|middleware`; outcome `ok|error`; middleware также `missing|invalid|expired|claims_decode`.
  - `sessions_op_duration_seconds{op}` — histogram wall-clock latency.
- `WithLogger(*slog.Logger)` — silent по умолчанию; нужен только для panic-recovery в hooks.
- `WithOnIssue(fn)` — после успешного `Issue` (cookie + Store committed). Подключите для Sentry user-scope binding / welcome-email.
- `WithOnLogout(fn)` — после успешного `Logout` (cookie-driven) и `RevokeByID` (admin-driven). `subject` пустой когда cookie не был передан.
- `WithOnLogoutEverywhere(fn)` — после успешного bulk-revoke'а; `count` — число revoked sessions (-1 если Store не реализует Lister).
- `WithOnExpire(fn)` — fires внутри `Middleware` когда найдена и удалена истёкшая сессия. Отличается от `OnLogout` чтобы SIEM мог разделить "user logout" vs "session timeout".

Все хуки panic-safe — panic recovered + WARN-logged через `WithLogger`.

## Admin-поверхность через Lister

`SessionStore`-implementations МОГУТ реализовывать `Lister` (optional interface). Менеджер lazily type-asserts на него:

```go
type Lister interface {
    ListBySubject(ctx, subject) ([]Session, error)
    Stats(ctx) (StoreStats, error)
}
```

`MemoryStore` и `sessionsredis.Store` оба реализуют Lister. `StoreStats{Active, Total}`: `Active` — сессии с `ExpiresAt > now` и всё ещё видимые store'у, `Total` — все enumerable rows. Поля `Expired` нет специально: auto-evicting backend'ы (Redis EXPIREAT, in-memory с background GC) удаляют истёкшие записи до того, как Stats их видит, поэтому "сколько expired" — не cross-backend вопрос. Если нужны индивидуальные истёкшие сессии — используйте `ListBySubject` и фильтруйте по `Session.ExpiresAt`. Используйте из admin-эндпоинтов для рендеринга "active sessions for user X" / "force-revoke this specific session" интерфейсов.

## Ограничения

- **Не передаёт CSRF-token автоматически**. SameSite=Lax покрывает большинство CSRF-vectors, но для CSRF-paired form'ы — generate token at Issue, store в `Session.Claims`, validate в submit-handler.
- **Не делает device-fingerprinting**. Каждая Issue → новая session-id. Track devices через separate column в custom-claims структуре.
- **DeleteForSubject — best-effort на multi-Redis-shard'ах**. SET per subject держится в одном Redis-instance'е; для cluster'а partition'ить по subject-hash'у.

## См. также

- [`auth`](../README.md) — JWT + refresh + api-keys
- [`auth/refreshredis`](../refreshredis/README.md) — родственный паттерн для JWT-refresh-store
- [`clients/redis`](../../clients/redis/README.md) — Redis-client lifecycle
</content>
