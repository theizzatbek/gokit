package sqb_test

import (
	"testing"
	"time"

	"github.com/theizzatbek/gokit/db/sqb"
)

// FuzzDecodeCursor exercises the pagination-cursor parser. Cursors are
// echoed back by clients, so DecodeCursor sees untrusted input: it must
// never panic, and anything it accepts must round-trip through Encode.
func FuzzDecodeCursor(f *testing.F) {
	valid := sqb.Cursor{CreatedAt: time.Unix(1_700_000_000, 0).UTC(), ID: "user_42"}
	f.Add(valid.Encode())
	f.Add("")
	f.Add("not-base64-$$$")
	f.Add("e30=")     // base64 of "{}"
	f.Add("bnVsbA==") // base64 of "null"

	f.Fuzz(func(t *testing.T, s string) {
		cur, err := sqb.DecodeCursor(s)
		if err != nil {
			return
		}
		again, err2 := sqb.DecodeCursor(cur.Encode())
		if err2 != nil || !again.CreatedAt.Equal(cur.CreatedAt) || again.ID != cur.ID {
			t.Fatalf("cursor round-trip broke for %q: (err=%v) %+v vs %+v", s, err2, cur, again)
		}
	})
}
