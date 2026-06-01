# clients/email

Pluggable transactional-email kit. Один `Sender` interface фронтит
SMTP, AWS SES и Postmark backends — service-код не зависит от
provider'а, ops переключают SMTP→SES одним env-флагом.

**Импорт:** `github.com/theizzatbek/gokit/clients/email`
**Зависит от:** `aws-sdk-go-v2/service/sesv2` (для SES) + `errs` + `prometheus/client_golang`

## Quickstart

```go
import "github.com/theizzatbek/gokit/clients/email"

sender, _ := email.New(email.Config{
    Backend: "smtp",
    SMTP: email.SMTPConfig{
        Host: "smtp.sendgrid.net", Port: 587,
        Username: "apikey", Password: os.Getenv("SENDGRID_API_KEY"),
    },
}, email.WithLogger(svc.Logger()), email.WithMetrics(reg))

err := sender.Send(ctx, email.Message{
    From: email.Address{Email: "no-reply@app.io", Name: "App"},
    To:   []email.Address{{Email: "alice@example.com"}},
    Subject:  "Welcome",
    HTMLBody: "<h1>Hi Alice!</h1>",
    TextBody: "Hi Alice!",
})
```

## Backends

| Backend | Config-блок | Заметки |
|---|---|---|
| `smtp` | `SMTPConfig` | stdlib `net/smtp` + STARTTLS. Универсальный — работает с любым SMTP-relay'ем (SendGrid SMTP-mode, Mailgun, self-hosted). |
| `ses` | `SESConfig` | aws-sdk-go-v2/service/sesv2. Reuse'ит default-credential-chain (env, instance-profile, IRSA). |
| `postmark` | `PostmarkConfig` | HTTP API через `net/http`. Honours `MessageStream` (transactional vs broadcast). |
| `stub` | — | In-memory capture для тестов. `s.Sent()` → captured-Message-list. |

## Message contract

| Поле | Required | Заметки |
|---|---|---|
| `From.Email` | ✓ | RFC 5322-mailbox. `Name` — optional. |
| `To` / `CC` / `BCC` | ≥1 хотя бы в одном | По крайней мере один recipient. |
| `Subject` | ✓ | Non-empty. |
| `HTMLBody` ИЛИ `TextBody` | ≥1 | Можно оба → SMTP/SES шлют как multipart/alternative. |
| `Headers` | — | Map → `key: value` RFC 5322-headers. |
| `Attachments` | — | Stream через `io.Reader`; читается единожды at Send time. |
| `Tag` | — | Backend-specific tracking tag (Postmark `Tag`, SES `EmailTags`, SMTP `X-Tag`). |

`Message.Validate()` enforces контракт; `Send` валидирует автоматически.

## Templates

Опционально:

```go
ts := email.NewTemplates()
//go:embed templates/*
var tplFS embed.FS
_ = ts.LoadFS(tplFS, "templates")

var msg email.Message
_ = ts.Render("welcome", map[string]string{"Name": "Alice"}, &msg)
msg.From = email.Address{Email: "no-reply@app.io"}
msg.To = []email.Address{{Email: "alice@example.com"}}
msg.Subject = "Welcome"
_ = sender.Send(ctx, msg)
```

Convention: `welcome.html.tmpl` + `welcome.txt.tmpl` оба загружаются под именем `welcome`; missing-side даёт пустой body (Validate всё равно требует хотя бы один non-empty body).

## Опции

| Опция | Заметки |
|---|---|
| `WithLogger(*slog.Logger)` | Debug на successful Send, Warn на error. |
| `WithMetrics(reg)` | `email_send_total{backend, outcome}`, `email_send_duration_seconds{backend}`. |

## Error-mapping

| Случай | `*errs.Error` |
|---|---|
| Empty `Backend` / unknown backend | `KindValidation`, `email_invalid_config` |
| `Message.Validate` reject | `KindValidation`, `email_invalid_message` |
| Backend send-error | `KindUnavailable`, `email_send_failed` |
| Postmark 401/403 | `KindPermission`, `email_send_failed` |
| Postmark 429 | `KindRateLimited`, `email_send_failed` |
| Template not registered | `KindValidation`, `email_template_not_found` |
| Template exec failure | `KindValidation`, `email_template_exec_failed` |

## См. также

- [`clients/httpc`](../httpc/README.md) — Postmark можно переключить на kit'овский transport через `PostmarkConfig.HTTPClient`
- [`db/jobs`](../../db/jobs/README.md) — типичный паттерн "запланировать email через 30 мин"
</content>
