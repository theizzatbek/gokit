package auth

import "github.com/gofiber/fiber/v2"

// IPExtractor pulls the originating IP from a *fiber.Ctx. The default
// (when no [WithIPExtractor] is wired) is `c.IP()`, which honours
// fiber's `ProxyHeader` config. Override when the service runs behind
// a CDN / proxy that uses a different header (`CF-Connecting-IP`,
// `X-Real-IP`, `Fly-Client-IP`, …) and you do NOT want every request
// to mutate `app.Config.ProxyHeader`.
//
// Used uniformly across the auth package:
//
//   - security log `ip` attribute
//   - refresh-token Record.IP audit field
//   - rate-limit [KeyByIP] / [KeyBySubject] fallback bucket key
//
// Implementations MUST be cheap (every request calls them) and stable
// (returned string keys rate-limit buckets). An empty return is
// allowed — downstream treats it as "anonymous" and rate-limits all
// such requests under the same bucket.
type IPExtractor func(c *fiber.Ctx) string

// WithIPExtractor overrides the default IP-extraction strategy for the
// whole Auth bundle. Pass nil to restore the default (c.IP()).
//
//	a, _ := auth.New[Claims](cfg,
//	    auth.WithIPExtractor(func(c *fiber.Ctx) string {
//	        if v := c.Get("CF-Connecting-IP"); v != "" { return v }
//	        return c.IP()
//	    }),
//	)
func WithIPExtractor(fn IPExtractor) Option {
	return func(o *options) { o.ipExtractor = fn }
}

// clientIP is the canonical kit-side resolver — falls back to fiber's
// stdlib IP() when no extractor is wired or when the extractor returns
// empty.
func (a *Auth[C]) clientIP(c *fiber.Ctx) string {
	if a != nil && a.ipExtractor != nil {
		if v := a.ipExtractor(c); v != "" {
			return v
		}
	}
	return c.IP()
}
