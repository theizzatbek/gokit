// Package email is a pluggable transactional-email kit. A single
// Sender interface fronts SMTP, AWS SES, and Postmark backends so
// service code never depends on the chosen provider; ops can switch
// SMTP → SES with one config flag.
//
//	sender, err := email.New(cfg, email.WithLogger(svc.Logger()),
//	    email.WithMetrics(svc.MetricsRegistry()))
//	err = sender.Send(ctx, email.Message{
//	    From: email.Address{Email: "no-reply@app.io"},
//	    To:   []email.Address{{Email: "alice@example.com"}},
//	    Subject:  "Welcome",
//	    HTMLBody: "<h1>Hi Alice!</h1>",
//	    TextBody: "Hi Alice!",
//	})
//
// Backends:
//
//   - Backend "smtp"     — net/smtp via STARTTLS. Stdlib-only.
//   - Backend "ses"      — AWS SES v2 (sesv2.SendEmail). Reuses
//     aws-sdk-go-v2 config; honours IRSA / static creds.
//   - Backend "postmark" — Postmark HTTP API via clients/httpc. Uses
//     X-Postmark-Server-Token auth.
//   - Backend "stub"     — captures into Sent so tests can assert on
//     outbound messages without a live transport.
//
// Templates: optional html/template + text/template rendering via
// [Templates.Render]. Templates live as text files on disk; pass a
// glob or fs.FS to [LoadTemplates]. Kept lightweight — the kit is a
// transport, not a campaign tool.
package email
