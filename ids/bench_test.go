package ids_test

import (
	"testing"

	"github.com/theizzatbek/gokit/ids"
)

// sinkID defeats dead-code elimination for results that would otherwise
// be discarded.
var sinkID string

// BenchmarkNew measures minting a prefixed ULID — done at least once per
// created entity.
func BenchmarkNew(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkID = ids.New("user_")
	}
}

// BenchmarkParse measures validating + stripping an ID, the per-request
// cost when decoding path/body IDs into raw bytes for storage.
func BenchmarkParse(b *testing.B) {
	const id = "user_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ids.Parse("user_", id); err != nil {
			b.Fatal(err)
		}
	}
}
