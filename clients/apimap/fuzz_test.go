package apimap_test

import (
	"testing"

	"github.com/theizzatbek/gokit/clients/apimap"
)

// FuzzLoadBytes feeds arbitrary bytes to the upstream-API YAML parser.
// Contract: LoadBytes returns an error or nil, never panics.
func FuzzLoadBytes(f *testing.F) {
	seeds := []string{
		"",
		"clients: []",
		"clients:\n  - name: x\n    base_url: http://localhost\n",
		"clients:\n  - {}\n",
		"\x00bad: [",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = apimap.New().LoadBytes(data)
	})
}
