package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/theizzatbek/gokit/errs"
)

// Signer produces the outbound HMAC signature header. The format is
// fixed for the whole kit (Stripe-style):
//
//	X-Webhook-Signature: t=<unix>,v1=<hex(hmac-sha256(t + "." + body, secret))>
//
// Stateless — share one *Signer across goroutines.
type Signer struct{}

// SignatureHeader is the canonical header name produced by Sign.
const SignatureHeader = "X-Webhook-Signature"

// Sign returns the value of the signature header. Secret must be
// non-empty (CodeMissingSecret on violation).
func (s *Signer) Sign(body []byte, secret string, now time.Time) (string, error) {
	if secret == "" {
		return "", errs.Validation(CodeMissingSecret, "webhook: empty secret")
	}
	ts := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil)), nil
}
