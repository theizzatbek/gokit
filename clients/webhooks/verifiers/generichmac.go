package verifiers

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"hash"
	"strconv"
	"strings"
	"time"

	"github.com/theizzatbek/gokit/clients/webhooks"
	"github.com/theizzatbek/gokit/errs"
)

// HashAlgo enumerates the supported HMAC hash functions.
type HashAlgo int

const (
	HashSHA256 HashAlgo = iota
	HashSHA1
)

// Encoding enumerates how the HMAC bytes are rendered in the header.
type Encoding int

const (
	EncodingHex Encoding = iota
	EncodingBase64
)

// GenericHMACConfig describes a partner's HMAC signature scheme.
// Most providers fit into this shape — see verifiers.NewGitHub for a
// concrete preset.
//
// If TimestampHeader is non-empty the verifier reads the inbound
// timestamp, checks |now - ts| <= TimestampWindow (default 5m), and
// signs HMAC(ts + "." + body). Otherwise it signs HMAC(body) and
// skips the window check.
type GenericHMACConfig struct {
	Secret          []byte
	SignatureHeader string
	Algo            HashAlgo
	Encoding        Encoding
	Prefix          string        // optional, e.g. "sha256="
	TimestampHeader string        // optional, "" disables ts-mode
	TimestampWindow time.Duration // default 5m
}

type genericHMAC struct {
	cfg GenericHMACConfig
}

// NewGenericHMAC validates the config and returns a webhooks.Verifier.
func NewGenericHMAC(cfg GenericHMACConfig) (webhooks.Verifier, error) {
	if len(cfg.Secret) == 0 {
		return nil, errs.Validation(webhooks.CodeMissingSecret, "verifier: empty secret")
	}
	if cfg.SignatureHeader == "" {
		return nil, errs.Validation(webhooks.CodeSignatureInvalid, "verifier: SignatureHeader required")
	}
	if cfg.TimestampHeader != "" && cfg.TimestampWindow == 0 {
		cfg.TimestampWindow = 5 * time.Minute
	}
	return &genericHMAC{cfg: cfg}, nil
}

func (g *genericHMAC) Verify(headers map[string][]string, body []byte, now time.Time) error {
	sigHeader := firstHeader(headers, g.cfg.SignatureHeader)
	if sigHeader == "" {
		return errs.Unauthorized(webhooks.CodeSignatureInvalid, "missing signature header")
	}
	sigHeader = strings.TrimPrefix(sigHeader, g.cfg.Prefix)

	var signedPayload []byte
	if g.cfg.TimestampHeader != "" {
		tsStr := firstHeader(headers, g.cfg.TimestampHeader)
		if tsStr == "" {
			return errs.Unauthorized(webhooks.CodeSignatureInvalid, "missing timestamp header")
		}
		tsInt, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil {
			return errs.Unauthorized(webhooks.CodeSignatureInvalid, "bad timestamp")
		}
		ts := time.Unix(tsInt, 0)
		if absDur(now.Sub(ts)) > g.cfg.TimestampWindow {
			return errs.Unauthorized(webhooks.CodeSignatureInvalid, "timestamp outside window")
		}
		signedPayload = []byte(tsStr + ".")
		signedPayload = append(signedPayload, body...)
	} else {
		signedPayload = body
	}

	want := g.computeMAC(signedPayload)
	got, err := g.decode(sigHeader)
	if err != nil {
		return errs.Unauthorized(webhooks.CodeSignatureInvalid, "bad signature encoding")
	}
	if subtle.ConstantTimeCompare(want, got) != 1 {
		return errs.Unauthorized(webhooks.CodeSignatureInvalid, "signature mismatch")
	}
	return nil
}

func (g *genericHMAC) computeMAC(payload []byte) []byte {
	var h hash.Hash
	switch g.cfg.Algo {
	case HashSHA1:
		h = hmac.New(sha1.New, g.cfg.Secret)
	default:
		h = hmac.New(sha256.New, g.cfg.Secret)
	}
	h.Write(payload)
	return h.Sum(nil)
}

func (g *genericHMAC) decode(s string) ([]byte, error) {
	switch g.cfg.Encoding {
	case EncodingBase64:
		return base64.StdEncoding.DecodeString(s)
	default:
		return hex.DecodeString(s)
	}
}

func firstHeader(headers map[string][]string, name string) string {
	if vs, ok := headers[name]; ok && len(vs) > 0 {
		return vs[0]
	}
	// case-insensitive fallback
	for k, vs := range headers {
		if strings.EqualFold(k, name) && len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
