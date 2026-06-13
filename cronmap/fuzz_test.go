package cronmap_test

import (
	"testing"

	"github.com/theizzatbek/gokit/cronmap"
)

// FuzzLoadBytes feeds arbitrary bytes to the crons.yaml parser.
// Contract: LoadBytes returns an error or nil, never panics.
func FuzzLoadBytes(f *testing.F) {
	seeds := []string{
		"",
		"jobs: []",
		"jobs:\n  - name: nightly\n    schedule: '@daily'\n    handler: h\n",
		"jobs:\n  - {}\n",
		"\x00bad: [",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = cronmap.New().LoadBytes(data)
	})
}
