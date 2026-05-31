package fibermap

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
)

// Default values for [SecurityHeaders].
const (
	defaultHSTSMaxAge     = 31_536_000 // 1 year — Mozilla / OWASP recommended baseline
	defaultCSP            = "default-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
	defaultReferrer       = "strict-origin-when-cross-origin"
	defaultFrameOptions   = "DENY"
	defaultContentNoSniff = "nosniff"
)

type securityHeadersConfig struct {
	hstsEnabled       bool
	hstsMaxAge        int
	hstsIncludeSubDom bool
	hstsPreload       bool
	csp               string
	cspEnabled        bool
	frameOptions      string
	referrerPolicy    string
	contentNoSniff    bool
}

// SecurityHeadersOption tunes [SecurityHeaders] response headers.
type SecurityHeadersOption func(*securityHeadersConfig)

// WithHSTSMaxAge overrides the Strict-Transport-Security max-age
// directive (seconds). Default is 1 year. HSTS is on by default —
// disable via [WithoutHSTS] when the deployment isn't HTTPS-only.
func WithHSTSMaxAge(seconds int) SecurityHeadersOption {
	return func(c *securityHeadersConfig) { c.hstsMaxAge = seconds }
}

// WithHSTSIncludeSubdomains appends "includeSubDomains" to the
// HSTS header — every subdomain of this origin must also be
// HTTPS-only. Off by default because turning it on retroactively
// breaks any plain-HTTP subdomain.
func WithHSTSIncludeSubdomains() SecurityHeadersOption {
	return func(c *securityHeadersConfig) { c.hstsIncludeSubDom = true }
}

// WithHSTSPreload appends "preload" to the HSTS header. Only set
// once the domain is registered at https://hstspreload.org and
// includeSubDomains is also enabled — browsers reject preload
// without it.
func WithHSTSPreload() SecurityHeadersOption {
	return func(c *securityHeadersConfig) { c.hstsPreload = true }
}

// WithoutHSTS suppresses the Strict-Transport-Security header.
// Use when the service is reachable over plain HTTP by design
// (internal-only, dev cluster). Other headers remain installed.
func WithoutHSTS() SecurityHeadersOption {
	return func(c *securityHeadersConfig) { c.hstsEnabled = false }
}

// WithCSP overrides the default Content-Security-Policy value.
// The default policy is API-friendly:
// `default-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`
//
// For services that render HTML, tighten further (script-src,
// style-src, img-src). For pure JSON APIs, the default is the
// safe minimum.
func WithCSP(policy string) SecurityHeadersOption {
	return func(c *securityHeadersConfig) {
		c.csp = policy
		c.cspEnabled = policy != ""
	}
}

// WithoutCSP suppresses the Content-Security-Policy header.
// Other headers remain installed.
func WithoutCSP() SecurityHeadersOption {
	return func(c *securityHeadersConfig) { c.cspEnabled = false }
}

// WithFrameOptions overrides X-Frame-Options. Default "DENY".
// Common alternative: "SAMEORIGIN" for services that legitimately
// embed themselves. CSP frame-ancestors is the modern equivalent
// and is set via [WithCSP].
func WithFrameOptions(value string) SecurityHeadersOption {
	return func(c *securityHeadersConfig) { c.frameOptions = value }
}

// WithReferrerPolicy overrides Referrer-Policy. Default
// "strict-origin-when-cross-origin" — same-origin requests get
// the full URL, cross-origin gets only the origin, HTTP requests
// from HTTPS pages get nothing.
func WithReferrerPolicy(value string) SecurityHeadersOption {
	return func(c *securityHeadersConfig) { c.referrerPolicy = value }
}

// SecurityHeaders returns a Fiber middleware that adds the OWASP
// baseline response headers:
//
//   - Strict-Transport-Security (HSTS, 1 year max-age)
//   - X-Content-Type-Options: nosniff
//   - X-Frame-Options: DENY
//   - Referrer-Policy: strict-origin-when-cross-origin
//   - Content-Security-Policy (API-friendly default)
//
// Every header is opt-out individually via the matching With*
// option. Headers are written via [fiber.Ctx.Set] in c.Next()'s
// hook so they survive even on error responses (mounted before
// the user error handler emits the body).
//
// Mount at the App level so /metrics, /healthz, /readyz also
// carry the headers:
//
//	app.Use(fibermap.SecurityHeaders())
//
// service.New auto-installs this by default; pass
// service.WithoutSecurityHeaders to disable, or
// service.WithSecurityHeaders(opts...) to override the defaults.
func SecurityHeaders(opts ...SecurityHeadersOption) fiber.Handler {
	cfg := &securityHeadersConfig{
		hstsEnabled:    true,
		hstsMaxAge:     defaultHSTSMaxAge,
		csp:            defaultCSP,
		cspEnabled:     true,
		frameOptions:   defaultFrameOptions,
		referrerPolicy: defaultReferrer,
		contentNoSniff: true,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	hstsValue := buildHSTSValue(cfg)
	return func(c *fiber.Ctx) error {
		if cfg.hstsEnabled && hstsValue != "" {
			c.Set(fiber.HeaderStrictTransportSecurity, hstsValue)
		}
		if cfg.contentNoSniff {
			c.Set(fiber.HeaderXContentTypeOptions, defaultContentNoSniff)
		}
		if cfg.frameOptions != "" {
			c.Set(fiber.HeaderXFrameOptions, cfg.frameOptions)
		}
		if cfg.referrerPolicy != "" {
			c.Set(fiber.HeaderReferrerPolicy, cfg.referrerPolicy)
		}
		if cfg.cspEnabled && cfg.csp != "" {
			c.Set(fiber.HeaderContentSecurityPolicy, cfg.csp)
		}
		return c.Next()
	}
}

// buildHSTSValue assembles the Strict-Transport-Security header
// value from cfg. Returns "" when max-age is non-positive (header
// will be skipped at write time).
func buildHSTSValue(cfg *securityHeadersConfig) string {
	if cfg.hstsMaxAge <= 0 {
		return ""
	}
	v := "max-age=" + strconv.Itoa(cfg.hstsMaxAge)
	if cfg.hstsIncludeSubDom {
		v += "; includeSubDomains"
	}
	if cfg.hstsPreload {
		v += "; preload"
	}
	return v
}
