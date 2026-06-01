package natsgw

import (
	"context"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/clients/natsmap"
)

// CustomHandler is a full override of the gateway's publish step.
// When installed via [WithCustomHandler], the kit runs the standard
// pipeline (subject extraction, allowlist, body cap, validators) and
// then hands control to fn instead of calling natsmap.PublishRaw
// directly.
//
// Use cases:
//
//   - Tee one inbound publish to multiple NATS subjects
//     (analytics + auditlog, primary + dr region, etc.).
//   - Persist the payload (db, S3) before — or instead of —
//     pushing onto NATS.
//   - Transform the payload (add server-side timestamp, redact PII,
//     re-encode) on the way out.
//   - Conditionally publish (skip publishes whose body matches a
//     deny rule, route by content to a different subject).
//   - Return a custom response code (e.g. 200 with a JSON body
//     describing the durable id).
//
// The supplied rt is the kit-built natsmap.Runtime; call
// natsmap.PublishRaw on it for the standard path. fc is the live
// Fiber context — write a custom response body via fc.JSON / fc.Send
// before returning if you want a non-202 reply.
//
// Returning nil → kit returns the default success status (configured
// via [WithStatusOK], default 202) UNLESS the handler already wrote
// to fc. Returning an error → handler renders it via the kit's
// error middleware (preserves the Code on *errs.Error).
//
// Headers passed to the handler are the gateway's collected map
// (X-Request-ID auto-injected, plus [WithHeaderForwarder] entries).
// The handler is free to mutate before publishing.
type CustomHandler func(ctx context.Context, fc *fiber.Ctx, rt *natsmap.Runtime, subject string, body []byte, headers map[string][]string) error

// WithCustomHandler installs a custom publish step. fn fully replaces
// the kit's natsmap.PublishRaw call; the rest of the pipeline
// (subject, allowlist, body cap, validators, header collection,
// success status, response) is unchanged.
//
// Only one custom handler may be active at a time — repeated calls
// last-write-wins. For composition (tee + transform + audit) stack
// the logic INSIDE one handler instead of layering options.
func WithCustomHandler(fn CustomHandler) Option {
	return func(c *config) { c.customHandler = fn }
}
