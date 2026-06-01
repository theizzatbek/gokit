package links

import (
	"testing"

	"github.com/go-playground/validator/v10"
)

// holder lets us drive validateSafeURL via the standard validator API
// (which expects a struct, not a bare string).
type holder struct {
	URL string `validate:"safe_url"`
}

func newValidator(t *testing.T) *validator.Validate {
	t.Helper()
	v := validator.New()
	if err := RegisterValidators(v); err != nil {
		t.Fatalf("RegisterValidators: %v", err)
	}
	return v
}

func TestSafeURL_AcceptsPublicURLs(t *testing.T) {
	v := newValidator(t)
	cases := []string{
		"https://example.com",
		"http://example.com/path?q=1",
		"https://en.wikipedia.org/wiki/URL",
		"https://nytimes.com",
		"https://8.8.8.8/dns",                  // public IP literal
		"https://[2001:4860:4860::8888]/v6dns", // public IPv6 literal
	}
	for _, u := range cases {
		if err := v.Struct(holder{URL: u}); err != nil {
			t.Errorf("expected accept %q, got %v", u, err)
		}
	}
}

func TestSafeURL_RejectsLoopbackAndPrivate(t *testing.T) {
	v := newValidator(t)
	cases := []struct {
		name, url string
	}{
		{"loopback IPv4", "http://127.0.0.1/admin"},
		{"loopback IPv4 (other byte)", "http://127.250.42.7/x"},
		{"loopback IPv6", "http://[::1]/admin"},
		{"localhost name", "http://localhost/admin"},
		{"localhost.localdomain", "http://localhost.localdomain/"},
		{"RFC1918 10/8", "http://10.0.0.5/internal"},
		{"RFC1918 172.16/12", "http://172.20.1.1/internal"},
		{"RFC1918 192.168/16", "http://192.168.1.1/router"},
		{"IPv6 ULA", "http://[fd00::1]/private"},
		{"link-local v4", "http://169.254.169.254/latest/meta-data"}, // classic SSRF target
		{"link-local v6", "http://[fe80::1]/x"},
		{"unspecified IPv4", "http://0.0.0.0/x"},
		{"unspecified IPv6", "http://[::]/x"},
		{"malformed URL", "not-a-url"},
		{"missing host", "https:///oops"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := v.Struct(holder{URL: tc.url}); err == nil {
				t.Errorf("expected reject %q, got nil", tc.url)
			}
		})
	}
}
