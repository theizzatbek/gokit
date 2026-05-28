package links

import (
	"net"
	"net/url"
	"strings"

	"github.com/go-playground/validator/v10"
)

// RegisterValidators registers links-package-specific validators on v.
// Wire this into service.WithValidator so RegisterHandlerWith* picks up
// the same instance.
//
// Currently registers:
//
//	safe_url — rejects URLs whose host is loopback, RFC1918 / IPv6 ULA
//	           private, link-local, the unspecified address 0.0.0.0,
//	           or the names "localhost" / "localhost.localdomain". Used
//	           on CreateRequest.URL to block SSRF-style abuse of the
//	           shortener (an attacker shortens http://10.0.0.5/admin
//	           and then anyone clicking the short URL hits the
//	           attacker-targeted internal endpoint).
func RegisterValidators(v *validator.Validate) error {
	return v.RegisterValidation("safe_url", validateSafeURL)
}

func validateSafeURL(fl validator.FieldLevel) bool {
	raw := fl.Field().String()
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "localhost", "localhost.localdomain", "":
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		if ip.IsLoopback() ||
			ip.IsPrivate() ||
			ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() ||
			ip.IsUnspecified() {
			return false
		}
	}
	return true
}
