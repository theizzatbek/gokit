package sqb_test

import (
	"testing"
	"time"

	"github.com/theizzatbek/gokit/db/sqb"
)

var benchCursor = sqb.Cursor{CreatedAt: time.Unix(1_700_000_000, 0).UTC(), ID: "user_01ARZ3NDEKTSV4RRFFQ69G5FAV"}

// sinkStr defeats dead-code elimination for the discarded Encode result.
var sinkStr string

// BenchmarkCursorEncode measures producing the opaque next-page token
// returned on every paginated list response.
func BenchmarkCursorEncode(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkStr = benchCursor.Encode()
	}
}

// BenchmarkCursorDecode measures parsing a client-supplied cursor on
// every paginated list request.
func BenchmarkCursorDecode(b *testing.B) {
	s := benchCursor.Encode()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := sqb.DecodeCursor(s); err != nil {
			b.Fatal(err)
		}
	}
}
