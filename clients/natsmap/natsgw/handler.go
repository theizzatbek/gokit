package natsgw

import (
	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/clients/natsmap"
	xerrs "github.com/theizzatbek/gokit/errs"
)

// Handler returns a Fiber handler that publishes the request body
// onto the NATS subject derived from the request — by default
// `c.Params("subject")`. The kit's X-Request-ID auto-propagates
// from ctx; additional headers can opt in via [WithHeaderForwarder].
//
// Defaults:
//
//   - Subject from path param `:subject` (override via
//     [WithSubjectExtractor]).
//   - Body is the raw payload — forwarded verbatim to
//     natsmap.PublishRaw, no transformation.
//   - Max body 1 MiB ([WithMaxBodySize]).
//   - Response 202 Accepted ([WithStatusOK]).
//   - No subject allowlist ([WithSubjectAllowlist] is opt-in).
//
// Wire under any Fiber router; auth + role gating belong upstream.
//
// Returns nil from the handler on success. All failure paths return
// *errs.Error so the kit's error middleware renders them with
// stable Code labels.
func Handler(rt *natsmap.Runtime, opts ...Option) fiber.Handler {
	cfg := config{
		subjectFn:    func(c *fiber.Ctx) string { return c.Params("subject") },
		maxBodyBytes: 1 << 20,
		statusOK:     fiber.StatusAccepted,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(c *fiber.Ctx) error {
		subject := cfg.subjectFn(c)
		if subject == "" {
			return xerrs.Validation(CodeInvalidSubject,
				"natsgw: subject is required")
		}
		if cfg.allowlist != nil && !cfg.allowlist[subject] {
			return xerrs.Validationf(CodeSubjectNotAllowed,
				"natsgw: subject %q not in allowlist", subject)
		}
		body := c.Body()
		if cfg.maxBodyBytes > 0 && len(body) > cfg.maxBodyBytes {
			return xerrs.Validationf(CodePayloadTooLarge,
				"natsgw: body %d bytes exceeds limit %d",
				len(body), cfg.maxBodyBytes)
		}
		var hdrs map[string][]string
		if len(cfg.forwardHeaders) > 0 {
			hdrs = make(map[string][]string, len(cfg.forwardHeaders))
			for _, name := range cfg.forwardHeaders {
				if v := c.Get(name); v != "" {
					hdrs[name] = []string{v}
				}
			}
		}
		if err := natsmap.PublishRaw(c.UserContext(), rt, subject, body, hdrs); err != nil {
			return xerrs.Wrap(err, xerrs.KindUnavailable, CodePublishFailed,
				"natsgw: publish failed")
		}
		return c.SendStatus(cfg.statusOK)
	}
}
