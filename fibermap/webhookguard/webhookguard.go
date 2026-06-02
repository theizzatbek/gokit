package webhookguard

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/theizzatbek/gokit/clients/webhooks"
)

// Option tunes [New].
type Option func(*config)

type config struct {
	bodyLimit int
	errorCode string
	clockSkew time.Duration
	now       func() time.Time
}

// WithBodyLimit caps request body bytes. Default 1 MiB.
func WithBodyLimit(n int) Option { return func(c *config) { c.bodyLimit = n } }

// WithErrorCode overrides the *errs.Error.Code returned on
// signature failure. Default: webhooks.CodeSignatureInvalid.
func WithErrorCode(code string) Option { return func(c *config) { c.errorCode = code } }

// WithClockSkew adjusts the Verifier-side wall clock; documented
// here so verifiers with timestamp checks share one knob with the
// middleware. Default 5m.
func WithClockSkew(d time.Duration) Option { return func(c *config) { c.clockSkew = d } }

const defaultBodyLimit = 1 << 20

// New returns a Fiber middleware that verifies the inbound payload.
func New(v webhooks.Verifier, opts ...Option) fiber.Handler {
	cfg := &config{
		bodyLimit: defaultBodyLimit,
		errorCode: webhooks.CodeSignatureInvalid,
		clockSkew: 5 * time.Minute,
		now:       time.Now,
	}
	for _, o := range opts {
		o(cfg)
	}
	return func(c *fiber.Ctx) error {
		body := c.Body()
		if cfg.bodyLimit > 0 && len(body) > cfg.bodyLimit {
			return fiber.ErrRequestEntityTooLarge
		}
		headers := map[string][]string{}
		c.Request().Header.VisitAll(func(k, v []byte) {
			headers[string(k)] = append(headers[string(k)], string(v))
		})
		if err := v.Verify(headers, body, cfg.now()); err != nil {
			return err
		}
		return c.Next()
	}
}
