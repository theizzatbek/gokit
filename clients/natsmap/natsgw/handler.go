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
		// Validators run after the cheap rejection paths (allowlist
		// + body cap) so a malformed flood doesn't pay for the
		// expensive JSON decode before being kicked. Per-subject
		// validators short-circuit on subject mismatch; subject ==
		// "" entries are global.
		ctx := c.UserContext()
		for _, v := range cfg.validators {
			if v.subject != "" && v.subject != subject {
				continue
			}
			if err := v.fn(ctx, subject, body); err != nil {
				// *errs.Error from the validator passes through
				// unchanged so its Code wins; plain errors get
				// wrapped with CodeValidationFailed.
				if _, ok := err.(*xerrs.Error); ok {
					return err
				}
				return xerrs.Wrap(err, xerrs.KindValidation, CodeValidationFailed,
					"natsgw: validation failed")
			}
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
		// Custom handler takes over the publish step entirely. It
		// MAY call natsmap.PublishRaw on rt, OR do something else
		// (tee, persist, transform, etc.). When it writes to fc
		// itself (custom status / body), the kit honours that;
		// otherwise we fall through to the default success status.
		if cfg.customHandler != nil {
			if err := cfg.customHandler(ctx, c, rt, subject, body, hdrs); err != nil {
				return err
			}
			// If the handler already wrote a response body the
			// kit does NOT overwrite it — the handler owns the
			// reply shape (e.g. "200 + {id: \"durable-key-...\"}").
			// An empty body means the handler delegated the
			// reply, so we send the configured success status.
			if len(c.Response().Body()) > 0 {
				return nil
			}
			return c.SendStatus(cfg.statusOK)
		}
		if err := natsmap.PublishRaw(c.UserContext(), rt, subject, body, hdrs); err != nil {
			return xerrs.Wrap(err, xerrs.KindUnavailable, CodePublishFailed,
				"natsgw: publish failed")
		}
		return c.SendStatus(cfg.statusOK)
	}
}
