# audit

Append-only audit-log infrastructure. Compliance-frameworks (SOC2, HIPAA, PCI-DSS, finance-регуляция) требуют tamper-evident-запись каждого privileged-action'а — кто что сделал, над чем, с каким outcome'ом, когда, откуда.

**Импорт:** `github.com/theizzatbek/gokit/audit` + `audit/auditpg` (default Postgres backend)

## Quickstart

```go
import (
    "github.com/theizzatbek/gokit/audit"
    "github.com/theizzatbek/gokit/audit/auditpg"
)

store := auditpg.New(svc.DB)
_ = auditpg.ApplySchema(ctx, svc.DB)

logger, _ := audit.New(store, audit.Config{
    ServiceName: "tasks",
    Logger:      svc.Logger(),
}, audit.WithHashChain())

// Typed convenience constructors:
_ = logger.Login(ctx, audit.Actor{Subject: "u-42", IP: c.IP()}, audit.Success)
_ = logger.Updated(ctx, actor, target, map[string]any{"plan": "pro"})
_ = logger.Denied(ctx, actor, target, "post.delete", "not_owner")

// Free-form:
_, _ = logger.Log(ctx, audit.Event{
    Action: "billing.invoice_downloaded",
    Actor:  actor, Target: target, Outcome: audit.Success,
    Metadata: map[string]any{"invoice_id": "inv-42"},
})
```

## Event shape

| Поле | Заметки |
|---|---|
| `ID` | UUID, server-set если zero. |
| `OccurredAt` | timestamp; server-set если zero. В chain-mode перезаписывается INSIDE chain-lock'а — обеспечивает monotonic-order. |
| `ServiceName` | Stamped автоматически из `Config.ServiceName`. |
| `Actor` | `{Subject, Type, IP, UA}` — кто. |
| `Action` | Verb: `user.login`, `post.delete`. Required. |
| `Target` | `{Type, ID, Name}` — что. |
| `Outcome` | `success` / `failure` / `denied`. Required. |
| `Metadata` | `map[string]any` — free-form context (request_id, diff, reason, ...). |
| `PrevHash` / `Hash` | Chain-links, populated в `WithHashChain` mode. |

## Outcome семантика

| Outcome | Когда использовать |
|---|---|
| `success` | Action прошёл успешно. |
| `failure` | Action attempted и упал на execution layer'е (DB error, downstream timeout). НЕ permission rejection. |
| `denied` | Auth-layer reject (missing scope/role, ownership check, rate-limit). Самые security-релевантные entries. |

## Hash-chain (`WithHashChain`)

Каждый Append линкует event с прошлым через SHA-256:

```
Hash[N] = SHA256(canonical_json(event[N]) || Hash[N-1])
```

Verify-функция walk'ит chain end-to-end. Tampering с любым полем любого event'а ломает все Hash'и downstream → auditor'ы видят первое broken-link.

```go
events, _ := logger.Query(ctx, audit.Filter{})
if err := audit.Verify(events); err != nil {
    // err.Code == audit.CodeChainBroken
    // chain tampered or events dropped
}
```

### Concurrency-model

Hash-chain serialization трёхслойная:

1. **In-process** — `Logger.chainMu` блокирует concurrent goroutines в одном процессе.
2. **Cross-process** — `Store.ChainLock` (для Postgres — db/lock advisory lock на key `audit:chain`). Два pod'а пишущих в одну таблицу serialize'ятся через PostgreSQL-lock.
3. **OccurredAt** — re-stamped INSIDE chain-lock'а, чтобы `ORDER BY occurred_at ASC` точно матчил chain-insert-order.

Пропускная способность падает (serial inserts), но chain integrity гарантирована. Use only when compliance demands it — большинству apps не нужно.

## Store-контракт

```go
type Store interface {
    Append(ctx, e *Event) error
    Query(ctx, f Filter) ([]Event, error)
    LastHash(ctx) ([]byte, error)         // chain-mode seed
    PurgeBefore(ctx, t time.Time) (int64, error)
    ChainLock(ctx) (release func(), err error)  // cross-process serialization
}
```

Default-backend — `audit/auditpg`. `audit.NewMemoryStore()` — in-process для тестов / dev'а (lost on restart).

## Filter

```go
type Filter struct {
    Actor      string
    Action     string    // trailing "*" wildcard supported
    TargetType string
    TargetID   string
    Outcome    Outcome
    From, To   time.Time
    Limit      int
    Offset     int
}
```

Filter compiles в SQL WHERE-clause; Postgres использует indexes на `actor_subject`, `action`, `(target_type, target_id)`, `occurred_at DESC`.

## Retention

```go
// Удалить events старше 90 days. Запускать periodically через db/jobs:
purged, _ := logger.PurgeBefore(ctx, time.Now().AddDate(0, 0, -90))
```

Compliance-policy обычно требует **≥7 years retention** для finance / **≥6 years** для HIPAA. Adjust `PurgeBefore`-cutoff соответственно.

## Опции

| Опция | Заметки |
|---|---|
| `WithHashChain()` | Enable tamper-evident chain. Cost: serial inserts. Default off. |

## Error-mapping

| Случай | `*errs.Error` |
|---|---|
| `New(nil)` | `audit_nil_store` |
| Missing Action / Outcome | `audit_invalid_event` |
| Store.Append failure | `audit_append_failed` |
| Store.Query failure | `audit_query_failed` |
| Store.PurgeBefore failure | `audit_purge_failed` |
| `Verify` chain broken | `audit_chain_broken` |

## Когда что выбирать

| Use-case | Tool |
|---|---|
| "Кто удалил этот post?" — admin-tool query | `audit` (this) |
| "Когда последний раз залогинился user X?" | `audit` (Filter Action="auth.login" Actor=X) |
| Application logs / debugging | `slog` + `otelkit` |
| Cross-service event bus | `db/outbox` + `clients/natsmap` |
| Tamper-evident financial-ledger | `audit` + `WithHashChain()` |

## Ограничения

- **PII в Metadata** — кит хранит as-is. Если regulator требует encryption-at-rest, layer'ите `pgcrypto` поверх `metadata`-column'а на DB-level.
- **Chain Verify is O(N)** — для миллионов rows это minutes. Periodic-batch'ами / window'ами лучше: verify last-7-days chunk против stored-checkpoint.
- **No DLQ** — Append failures возвращают error caller'у. Production должен wrap'нуть в retry-loop с downstream-alert'ом на consistent-failure.

## См. также

- [`db/lock`](../db/lock/README.md) — primitive для chain-lock'а
- [`db/jobs`](../db/jobs/README.md) — типичный wiring для retention-job'а
- [`errs`](../errs/README.md) — error-контракт
</content>
