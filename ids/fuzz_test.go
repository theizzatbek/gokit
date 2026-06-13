package ids_test

import (
	"testing"

	"github.com/theizzatbek/gokit/ids"
)

// FuzzParse checks two contracts on the prefixed-ULID parser: it never
// panics on arbitrary input, and any string it accepts round-trips
// (Parse → Format → Parse yields the same raw bytes).
func FuzzParse(f *testing.F) {
	seeds := []string{
		"user_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"user_01arz3ndektsv4rrffq69g5fav", // lowercase — Parse is case-insensitive
		"user_",
		"",
		"acct_01ARZ3NDEKTSV4RRFFQ69G5FAV", // wrong prefix
		"user_!!!!!!!!!!!!!!!!!!!!!!!!!!", // bad suffix
		"user_01ARZ3NDEKTSV4RRFFQ69G5FA",  // 25 chars
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		raw, err := ids.Parse("user_", s)
		if err != nil {
			return
		}
		again, err2 := ids.Parse("user_", ids.Format("user_", raw))
		if err2 != nil || again != raw {
			t.Fatalf("round-trip broke: %q -> %x -> (err=%v, %x)", s, raw, err2, again)
		}
	})
}
