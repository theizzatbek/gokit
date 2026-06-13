# audit/auditfm

`audit/auditfm` — per-handler adapter, wire'ит [`audit`](../README.md) emission в [`fibermap`](../../fibermap/README.md) registration. Один декоратор `Wrap[T](logger, spec, fn)` emit'ит audit event ПОСЛЕ возврата handler'а, derived из spec'а (Action / Target / Subject / Metadata) который caller декларировал next to handler-registration.

С v1.1.0.

## Quickstart

```go
import (
    "github.com/theizzatbek/gokit/audit"
    "github.com/theizzatbek/gokit/audit/auditfm"
    "github.com/theizzatbek/gokit/fibermap"
)

fibermap.RegisterHandler(svc.Engine, "license.revoke",
    auditfm.Wrap[AppCtx](svc.Audit, auditfm.Spec{
        Action: "license.revoke",
        SubjectFn: func(c *fiber.Ctx) string {
            p, _ := auth.From[Claims](c)
            return p.Subject
        },
        TargetFn: func(c *fiber.Ctx) audit.Target {
            return audit.Target{Type: "license", ID: c.Params("id")}
        },
        MetadataFn: func(c *fiber.Ctx) map[string]any {
            return map[string]any{"reason": c.Query("reason")}
        },
    }, h.RevokeLicense),
)
```

После handler'а kit автоматически emit'ит event:

```json
{
  "action": "license.revoke",
  "actor": {"subject": "user_abc", "ip": "10.0.0.1", "ua": "..."},
  "target": {"type": "license", "id": "lic_42"},
  "metadata": {"reason": "compliance"},
  "outcome": "success"
}
```

## Outcome classification (default)

| Handler return | Outcome |
|---|---|
| `nil` | `audit.Success` |
| `*errs.Error{Kind: KindUnauthorized}` | `audit.Denied` |
| `*errs.Error{Kind: KindPermission}` | `audit.Denied` |
| `*errs.Error{Kind: KindValidation}` | `audit.Failure` |
| any other error | `audit.Failure` |

Override через `Spec.OutcomeFn`:

```go
OutcomeFn: func(err error) audit.Outcome {
    if errors.Is(err, db.ErrConflict) { return audit.Denied }
    return auditfm.DefaultOutcome(err)  // fall through to kit default
}
```

## Typed-bind handlers (RegisterHandlerWithBody / WithParams / …)

`Wrap[T]` принимает `fibermap.HandlerFunc[T]` (untyped — plain `*Context[T]` argument). Для typed-bind helper'ов inline pattern через `Emit`:

```go
fibermap.RegisterHandlerWithParams(eng, "license.revoke",
    func(c *fibermap.Context[AppCtx], p RevokeParams) error {
        err := h.RevokeLicense(c, p)
        auditfm.Emit(c.Ctx, svc.Audit, auditfm.Spec{
            Action: "license.revoke",
            TargetFn: func(c *fiber.Ctx) audit.Target {
                return audit.Target{Type: "license", ID: p.ID}
            },
        }, err)
        return err
    })
```

`Emit` — low-level building block: вызывается after handler решил outcome. Никогда не возвращает error, никогда не panic'ит на emit-failure (audit-store failures → `Spec.Logger.Warn` если задан; handler path остается transparent).

Можно дважды звать на одном запросе ("started" + "finished" pair для long-running ops).

## vs `audit/auditmw`

| | `audit/auditmw` | `audit/auditfm` |
|---|---|---|
| Granularity | app-level (per-request) | per-handler |
| Mechanism | Fiber middleware (`app.Use(...)`) | Handler decorator (`auditfm.Wrap(...)`) |
| Action derivation | HTTP method/path default + caller-supplied classifier | Static per-handler declaration |
| Use case | «Audit каждый POST под /admin» blanket-policy | «Audit ЭТОТ endpoint С ЭТИМ Action/Target» |

Mixing — fine: `auditfm` runs inside handler scope, `auditmw` wraps whole request. `auditfm` для precision, `auditmw` для blanket coverage.

## Failure isolation

- **Audit emission outlives request ctx.** `Emit` derives emit-ctx из `context.Background()` чтобы append не отменился mid-flight когда response writes завершились. Tracing values теряются вместе с cancellation — audit это durable layer и не должен быть torn down request lifecycle'ом.
- **Audit-store failures НЕ влияют на handler.** `audit.Logger.Log` errors → `Spec.Logger.Warn` для ops observability. HTTP response это outcome handler'а, не audit-store'а.
- **Nil logger при construction'е → panic.** `Wrap(nil, ...)` panic'ит на registration time. Гэп от забытого logger'а surface'ится немедленно, не на первом privileged request'е. Empty `Spec.Action` → silent skip с Warn через `Spec.Logger` (если задан).

## Goroutine safety

`Wrap` returns goroutine-safe handler (`audit.Logger.Log` itself goroutine-safe). Spec held by closure — read-only after registration; не мутируйте Spec post-registration.

## См. также

- [`audit/`](../README.md) — Logger, Event constructors, Store interfaces.
- [`audit/auditmw/`](../auditmw/README.md) — middleware-уровневая обвязка.
- [`audit/auditpg/`](../auditpg/README.md) — Postgres-backed Store с hash-chain.
- [`fibermap/`](../../fibermap/README.md) — handler registration patterns.
