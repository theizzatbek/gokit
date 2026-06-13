package natsmap_test

import (
	"testing"

	"github.com/theizzatbek/gokit/clients/natsmap"
)

// FuzzLoadBytes feeds arbitrary bytes to the natsmap YAML parser
// (subscribers/publishers). Contract: returns an error or nil, no panic.
func FuzzLoadBytes(f *testing.F) {
	seeds := []string{
		"",
		"subscribers: []\npublishers: []",
		"subscribers:\n  - name: s\n    subject: foo.bar\n",
		"publishers:\n  - {}\n",
		"\x00bad: [",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = natsmap.New().LoadBytes(data)
	})
}
