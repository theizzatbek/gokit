// Package gateway is the HTTP→NATS bridge of urlshort-publisher.
// One handler accepts {subject, payload, headers?} JSON over HTTP
// and republishes the payload bytes via natsmap.PublishRaw on the
// supplied subject.
package gateway

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/clients/natsmap"
	xerrs "github.com/theizzatbek/gokit/errs"
	"github.com/theizzatbek/gokit/fibermap"
)

// Stable error Code constants surfaced by the gateway handler.
const (
	// CodeUnknownSubject — the inbound subject is not registered as
	// a publisher in configs/publishers.yaml. Refused with 404 so
	// typos surface immediately rather than being silently dropped.
	CodeUnknownSubject = "publisher_unknown_subject"

	// CodeInvalidPayload — request body wasn't valid JSON of the
	// expected shape (no subject / no payload).
	CodeInvalidPayload = "publisher_invalid_payload"
)

// Request is the wire shape POST /publish accepts. Payload is
// json.RawMessage so callers can send native JSON (the gateway
// forwards the bytes verbatim — downstream subscribers decode the
// same way they would on a direct natsmap path).
//
// Headers are optional and merge with the publisher's static headers
// from publishers.yaml. X-Request-ID auto-propagates from the
// inbound HTTP request (the kit's reqctx middleware puts it on the
// ctx; natsmap.PublishRaw stamps it on the NATS message).
type Request struct {
	Subject string              `json:"subject"`
	Payload json.RawMessage     `json:"payload"`
	Headers map[string][]string `json:"headers,omitempty"`
}

// Handler returns the Fiber handler bound to the supplied natsmap
// Runtime. Goroutine-safe — natsmap.PublishRaw handles the
// concurrent path internally.
//
// Mounted by configs/routes.yaml under POST /publish.
func Handler[T any](rt *natsmap.Runtime) fibermap.HandlerFunc[T] {
	return func(c *fibermap.Context[T]) error {
		var req Request
		if err := json.Unmarshal(c.Ctx.Body(), &req); err != nil {
			return xerrs.Wrap(err, xerrs.KindValidation, CodeInvalidPayload,
				"publisher: invalid JSON body")
		}
		if req.Subject == "" {
			return xerrs.Validation(CodeInvalidPayload,
				"publisher: subject is required")
		}
		if len(req.Payload) == 0 {
			return xerrs.Validation(CodeInvalidPayload,
				"publisher: payload is required")
		}
		if err := natsmap.PublishRaw(c.Ctx.UserContext(), rt, req.Subject,
			[]byte(req.Payload), req.Headers); err != nil {
			// Translate natsmap_unknown_publisher into the
			// gateway-facing 404 so the api logs the right Code.
			return xerrs.Wrap(err, xerrs.KindNotFound, CodeUnknownSubject,
				"publisher: unknown subject (not in publishers.yaml)")
		}
		return c.Ctx.SendStatus(fiber.StatusAccepted)
	}
}
