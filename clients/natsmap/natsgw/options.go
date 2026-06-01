package natsgw

import (
	"github.com/gofiber/fiber/v2"
)

// Option tunes [Handler].
type Option func(*config)

type config struct {
	subjectFn      func(*fiber.Ctx) string
	allowlist      map[string]bool
	forwardHeaders []string
	maxBodyBytes   int
	statusOK       int
	validators     []scopedValidator
}

// scopedValidator pairs a Validator with an optional subject scope.
// Empty subject means "applies to every request".
type scopedValidator struct {
	subject string // "" = global
	fn      Validator
}

// WithSubjectExtractor overrides the default path-param-based
// extraction (`c.Params("subject")`). Useful when:
//
//   - The subject lives in a header (`c.Get("X-Subject")`).
//   - The subject is encoded in the path with a custom param name.
//   - You need to derive the subject from the body (parse a JSON
//     field via `c.BodyParser`).
//
// Return an empty string to reject the request with
// [CodeInvalidSubject].
func WithSubjectExtractor(fn func(*fiber.Ctx) string) Option {
	return func(c *config) { c.subjectFn = fn }
}

// WithSubjectAllowlist restricts the subjects this handler will
// forward. Without it, ANY subject registered in publishers.yaml
// is publishable through the gateway — fine for trusted internal
// fleets, dangerous for public-facing ingestion.
//
// Pass exact subject strings; wildcards are NOT supported (NATS's
// own subject hierarchy can be used by registering each leaf
// explicitly).
func WithSubjectAllowlist(subjects ...string) Option {
	return func(c *config) {
		c.allowlist = make(map[string]bool, len(subjects))
		for _, s := range subjects {
			c.allowlist[s] = true
		}
	}
}

// WithHeaderForwarder copies named HTTP headers into the NATS
// message headers. Default: no headers forwarded.
//
// Subscribers see them via [natsclient.Msg.Headers] (subject to
// JetStream's header support).
//
// Note: kit's X-Request-ID auto-propagates from ctx regardless of
// this option — the inbound request's request-ID flows into the
// NATS message header without explicit configuration.
func WithHeaderForwarder(headers ...string) Option {
	return func(c *config) {
		c.forwardHeaders = append(c.forwardHeaders, headers...)
	}
}

// WithMaxBodySize caps the inbound payload. Default 1 MiB. Pass
// 0 to disable the cap (rare — the Fiber-level BodyLimit applies
// regardless and is the right place to set the global ceiling).
func WithMaxBodySize(n int) Option {
	return func(c *config) { c.maxBodyBytes = n }
}

// WithStatusOK overrides the success status. Default 202 Accepted —
// signals "we wrote the message; downstream processing is
// asynchronous". Use 200 OK when callers expect HTTP-style
// semantics; 204 No Content for fire-and-forget patterns.
func WithStatusOK(s int) Option {
	return func(c *config) { c.statusOK = s }
}
