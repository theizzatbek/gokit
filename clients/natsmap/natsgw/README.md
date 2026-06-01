# clients/natsmap/natsgw

Generic HTTP→NATS gateway middleware. Mount под Fiber-router'ом —
inbound-request'ы republish'атся на matching-NATS-subject через
`natsmap.PublishRaw`.

**Импорт:** `github.com/theizzatbek/gokit/clients/natsmap/natsgw`

## Quickstart

```go
import "github.com/theizzatbek/gokit/clients/natsmap/natsgw"

app.Post("/publish/:subject", natsgw.Handler(svc.NATSMap,
    natsgw.WithSubjectAllowlist(
        "urlshort.link.created",
        "urlshort.link.visited",
    ),
    natsgw.WithHeaderForwarder("X-Tenant"),
))
```

`POST /publish/urlshort.link.visited` с raw-body → publish'ится на NATS-subject `urlshort.link.visited`. Returns **202 Accepted** на success.

Через `service.WithNATSMapGateway(prefix, opts...)` — auto-mount без явного `app.Post`:

```go
svc, _ := service.New[App, Claims](ctx, cfg,
    service.WithNATSMap(),
    service.WithNATSMapGateway("/publish",
        natsgw.WithSubjectAllowlist("urlshort.link.created", "urlshort.link.visited"),
    ),
)
```

## Use-cases

| Pattern | Зачем |
|---|---|
| **Edge gateway** | HTTP-only сервисы publish'ат в NATS через gateway без import'а natsmap'а в свой binary. См. `examples/urlshort/urlshort-publisher`. |
| **Polyglot fleet** | Сервисы на языках без mature NATS-client'а (Ruby, PHP, edge-runtime'ы) POST'ят JSON вместо. |
| **Webhook ingestion** | External-systems POST'ят events напрямую в внутренний NATS-bus. |
| **Replay-tools** | Operator-script POST'ит captured-event-stream через gateway для replay'я в testing-env. |

## Defaults

| Aspect | Default | Override |
|---|---|---|
| Subject extraction | `c.Params("subject")` | `WithSubjectExtractor(fn)` |
| Subject allowlist | empty (any registered publisher allowed) | `WithSubjectAllowlist(subjects...)` |
| Header forwarding | none (only kit's X-Request-ID from ctx) | `WithHeaderForwarder(headers...)` |
| Max body | 1 MiB | `WithMaxBodySize(n)` |
| Success status | 202 Accepted | `WithStatusOK(code)` |

## Опции

| Опция | Заметки |
|---|---|
| `WithSubjectExtractor(fn)` | Custom extractor — например из header'а, body-field'а, или path-segment'а с другим именем. Empty-string-return → 400 `natsgw_invalid_subject`. |
| `WithSubjectAllowlist(subjects...)` | Restrict the publishable subjects. Без allowlist'а — ANY registered publisher OK (fine для trusted-internal, dangerous для public-facing). |
| `WithHeaderForwarder(headers...)` | Forward named HTTP-headers в NATS message-headers. |
| `WithMaxBodySize(n)` | Cap inbound-payload. `0` отключает (но Fiber-level BodyLimit всё ещё работает). |
| `WithStatusOK(code)` | Override success-status. 200 для HTTP-semantics, 204 для fire-and-forget. |
| `WithValidator(fn)` | Global validator — runs на каждый publish после allowlist + body-cap, до NATS publish. Stack multiple — first non-nil error wins. |
| `WithSubjectValidator(subject, fn)` | Per-subject validator. Skipped когда inbound subject не match'ит. Stack для multi-subject contracts. |

## Validators

Validator runs **AFTER** subject-allowlist + body-cap, **BEFORE** NATS-publish — cheap rejection paths first, expensive JSON-decode только если payload не отброшен раньше.

```go
type Validator func(ctx context.Context, subject string, body []byte) error
```

Non-nil error → request rejected с 400 + `natsgw_validation_failed`. Validator может вернуть собственный `*errs.Error` (любой Code) — kit preserves его без re-wrap'а; plain `error` wrap'ятся kit'ом.

### Helper'ы

| Helper | Заметки |
|---|---|
| `natsgw.ValidJSON()` | Cheap "is well-formed JSON"-check через `json.Valid`. Use для coarse-pre-check'а ("don't admit malformed JSON onto the bus"). |
| `natsgw.UnmarshalAs[T]()` | Typed per-subject validator: JSON-decodes body into zero-value `T`, decode-failure → rejection. Pairs с `WithSubjectValidator` для типизированного контракта. |

### Пример: typed per-subject validation

```go
import (
    "github.com/theizzatbek/gokit/clients/natsmap/natsgw"
    "yourapp/events"
)

natsgw.Handler(rt,
    natsgw.WithSubjectAllowlist(
        "urlshort.link.created",
        "urlshort.link.visited",
    ),
    natsgw.WithSubjectValidator("urlshort.link.created",
        natsgw.UnmarshalAs[events.LinkCreated]()),
    natsgw.WithSubjectValidator("urlshort.link.visited",
        natsgw.UnmarshalAs[events.LinkVisited]()),
)
```

Каждый payload checked'ится по correct shape'у *до* того, как hit'нет NATS — subscribers вниз по pipeline никогда не видят undecodable rows. Defense-in-depth: малicious / buggy producer не может flood'ить bus malformed-event'ами.

### Пример: custom-validator с domain-rule'ами

```go
natsgw.WithSubjectValidator("orders.placed",
    func(ctx context.Context, _ string, body []byte) error {
        var o Order
        if err := json.Unmarshal(body, &o); err != nil {
            return err
        }
        if o.AmountUSD < 0 {
            return xerrs.Validation("orders_negative_amount",
                "amount must be non-negative")
        }
        if o.Currency != "USD" && o.Currency != "EUR" {
            return xerrs.Validationf("orders_unsupported_currency",
                "currency %q not supported", o.Currency)
        }
        return nil
    })
```

## Error-mapping

| Случай | `*errs.Error` |
|---|---|
| Empty subject (extractor returned "") | 400 `natsgw_invalid_subject` |
| Subject не в allowlist'е | 400 `natsgw_subject_not_allowed` |
| Body > `WithMaxBodySize` | 400 `natsgw_payload_too_large` |
| Validator rejected | 400 `natsgw_validation_failed` (или validator's own Code, если он вернул `*errs.Error`) |
| Unknown publisher (subject not in publishers.yaml) | 404 `natsgw_publish_failed` (wraps `natsmap_unknown_publisher`) |
| NATS transport-blip | 503 `natsgw_publish_failed` |

## Security

**Без auth-middleware** — wire your auth + role-gate перед mount'ом:

```go
guarded := app.Group("/internal",
    svc.Auth.Bearer(auth.BearerRequired),
    svc.Auth.RequireRole("publisher"),
)
guarded.Post("/publish/:subject", natsgw.Handler(svc.NATSMap, ...))
```

Или wire `service.WithFiberMiddleware(authMW...)` глобально.

**Allowlist обязателен для public-facing**: без `WithSubjectAllowlist`-а любой subject в `publishers.yaml` доступен. Internal-trusted-fleet — OK, public-ingestion — strictly allowlist'нуть.

## Headers semantic

| Header | Поведение |
|---|---|
| `X-Request-ID` | Auto-propagates из ctx (kit's reqctx middleware) → NATS message-header. Не требует `WithHeaderForwarder`. |
| Custom (e.g. `X-Tenant`) | Forward only if `WithHeaderForwarder("X-Tenant")` is set. |
| `Authorization`, `Cookie`, etc. | **Никогда** не forward'ятся silently — explicit opt-in для каждого. |

## Ограничения

- **No request-body transformation** — gateway forwards bytes verbatim. Subscribers decode same way they would on direct natsmap-path. JSON-payload'ы encoded one way → published one way → decoded one way.
- **No subject-wildcards в allowlist'е**. NATS-side subject-hierarchy используется через explicit per-leaf registration.
- **No batching** — каждый HTTP-request → один NATS-publish. Для bulk-ingest'а: pre-batch на client-side, publish в один subject с repeated payload.
- **No backpressure** — gateway возвращает 202 как только NATS-publish-call OK. Downstream-consumer-saturation не back-propagate'ится.

## См. также

- [`clients/natsmap`](../README.md) — declarative NATS subscribers + publishers
- [`service.WithNATSMapGateway`](../../../service/README.md) — service-level auto-wire
- [`examples/urlshort/urlshort-publisher`](../../../examples/urlshort/urlshort-publisher/main.go) — production-pattern reference (edge-gateway + outbox-worker)
</content>
