package fibermap_test

import (
	"testing"

	"github.com/theizzatbek/gokit/fibermap"
)

// FuzzLint hammers the routes.yaml parser with arbitrary bytes. Contract:
// Lint always returns (a typed *Error or nil) and never panics — it runs
// on operator-supplied config, but malformed input must fail gracefully.
func FuzzLint(f *testing.F) {
	seeds := []string{
		"",
		"groups: []",
		"groups:\n  - prefix: /v1\n    routes:\n      - {method: GET, path: /x, handler: h}",
		"middleware_sets:\n  a: [b]\n  b: [a]", // set cycle
		"groups:\n  - routes:\n      - {method: BOGUS, path: /x, handler: h}",
		"groups:\n  - prefix: /v1\n    middleware: [{f: [1, 2]}]",
		":\n:::not yaml",
		"\x00\x01\x02",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = fibermap.Lint(data)
	})
}
