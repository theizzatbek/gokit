package webhookguard

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

func mustMAC(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
