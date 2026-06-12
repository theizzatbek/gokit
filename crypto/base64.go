package crypto

import (
	"encoding/base64"

	"github.com/theizzatbek/gokit/errs"
)

// decodeAnyBase64 accepts every base64 flavour Go's stdlib ships:
// standard / URL-safe, padded / raw. The first successful decoder
// wins; failure to decode under all four returns [CodeKeyBase64].
//
// Operators copy-pasting keys from key-management UIs see "just
// works" instead of having to remember which flavour the UI used —
// the four flavours have non-overlapping success domains for any
// non-trivial input.
func decodeAnyBase64(s string) ([]byte, error) {
	if s == "" {
		return nil, errs.Validation(CodeKeyBase64, "crypto: base64 key string is empty")
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.RawURLEncoding,
	} {
		if raw, err := enc.DecodeString(s); err == nil {
			return raw, nil
		}
	}
	return nil, errs.Validation(CodeKeyBase64,
		"crypto: base64 key — not valid under std/url, padded/raw (all flavours tried)")
}
