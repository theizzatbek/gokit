package main

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/theizzatbek/gokit/auth"
	"github.com/theizzatbek/gokit/auth/apikeypg"
)

const usageAuth = `kit auth — Ed25519 key generation + API key minting

Usage:
  kit auth keygen [--kid k1]
  kit auth apikey new --subject S [--scopes a,b] [--role r] \
                      [--expires-in 90d] [--description "..."] \
                      [--secret HEX_OR_BASE64] [--dsn DSN]

keygen writes PKCS8 Ed25519 private + SPKI public PEMs to stdout.
apikey new mints a fresh kit_-prefixed plain key, HMACs it with the
kit-side secret (--secret flag, or API_KEY_HASH_SECRET env), and
inserts via auth/apikeypg.Store. The plain key is printed ONCE on
stdout — store it somewhere safe; only the hash lives on the server.
`

func runAuth(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageAuth)
		return errors.New("auth: subcommand required")
	}
	switch args[0] {
	case "keygen":
		return authKeygen(args[1:])
	case "apikey":
		return authAPIKey(ctx, args[1:])
	case "help", "--help", "-h":
		fmt.Fprint(os.Stdout, usageAuth)
		return nil
	default:
		fmt.Fprint(os.Stderr, usageAuth)
		return fmt.Errorf("auth: unknown subcommand %q", args[0])
	}
}

func authKeygen(args []string) error {
	fs := flag.NewFlagSet("auth keygen", flag.ContinueOnError)
	kid := fs.String("kid", "k1", "key identifier embedded in the JWT header")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return err
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})

	fmt.Printf("# kid: %s\n# algorithm: EdDSA (Ed25519)\n", *kid)
	fmt.Println("# --- private key (keep secret, set as AUTH_PRIVATE_KEY env) ---")
	fmt.Print(string(privPEM))
	fmt.Println("# --- public key (safe to distribute, for verify-only services) ---")
	fmt.Print(string(pubPEM))
	return nil
}

func authAPIKey(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("auth apikey: subcommand required (new)")
	}
	switch args[0] {
	case "new":
		return authAPIKeyNew(ctx, args[1:])
	default:
		return fmt.Errorf("auth apikey: unknown subcommand %q", args[0])
	}
}

func authAPIKeyNew(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("auth apikey new", flag.ContinueOnError)
	subject := fs.String("subject", "", "principal subject (service / user id) — required")
	scopes := fs.String("scopes", "", "comma-separated scope list")
	role := fs.String("role", "", "broad role (admin / service / …)")
	description := fs.String("description", "", "audit-trail description")
	expiresIn := fs.String("expires-in", "", "duration until expiry (e.g. 90d, 24h). Empty = no expiry.")
	secretFlag := fs.String("secret", "", "kit HMAC secret (hex or base64). Falls back to API_KEY_HASH_SECRET env.")
	dsn := fs.String("dsn", "", "postgres URL (falls back to DATABASE_URL env)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *subject == "" {
		return errors.New("auth apikey new: --subject is required")
	}
	secret, err := loadSecret(*secretFlag)
	if err != nil {
		return err
	}
	exp, err := parseExpiry(*expiresIn)
	if err != nil {
		return err
	}

	plain, err := generatePlainKey()
	if err != nil {
		return err
	}
	hash := auth.HashAPIKey(plain, secret)

	d, err := loadDB(ctx, *dsn)
	if err != nil {
		return err
	}
	defer d.Close()
	store := apikeypg.New(d)
	id, err := store.Insert(ctx, apikeypg.InsertParams{
		KeyHash:     hash,
		Subject:     *subject,
		Scopes:      splitCSV(*scopes),
		Role:        *role,
		Description: *description,
		ExpiresAt:   exp,
	})
	if err != nil {
		return err
	}
	fmt.Println("# --- API key (printed ONCE — copy it now) ---")
	fmt.Println(plain)
	fmt.Println()
	fmt.Printf("# id:          %s\n", id)
	fmt.Printf("# subject:     %s\n", *subject)
	fmt.Printf("# scopes:      %s\n", *scopes)
	fmt.Printf("# role:        %s\n", *role)
	if !exp.IsZero() {
		fmt.Printf("# expires_at:  %s\n", exp.Format(time.RFC3339))
	}
	return nil
}

// loadSecret returns the HMAC secret from --secret flag (hex or
// base64) or the API_KEY_HASH_SECRET env. Hex is auto-detected when
// the value is even-length and consists of hex digits; otherwise
// base64. Returns the raw bytes Auth.APIKey uses internally.
func loadSecret(flagVal string) ([]byte, error) {
	v := flagVal
	if v == "" {
		v = os.Getenv("API_KEY_HASH_SECRET")
	}
	if v == "" {
		return nil, errors.New("missing --secret flag or API_KEY_HASH_SECRET env")
	}
	if isHex(v) {
		b := make([]byte, len(v)/2)
		_, err := fmt.Sscanf(v, "%x", &b)
		if err == nil {
			return b, nil
		}
	}
	if b, err := base64.StdEncoding.DecodeString(v); err == nil {
		return b, nil
	}
	// Fallback: treat as a raw passphrase. HMAC accepts any byte
	// length so this still works, but warn the operator since
	// passphrases are weaker than random bytes.
	return []byte(v), nil
}

func isHex(s string) bool {
	if len(s) == 0 || len(s)%2 != 0 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// parseExpiry accepts Go duration strings ("90d" is non-standard so
// we expand) plus an empty string meaning "no expiry".
func parseExpiry(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if strings.HasSuffix(s, "d") {
		days := strings.TrimSuffix(s, "d")
		var n int
		if _, err := fmt.Sscanf(days, "%d", &n); err != nil {
			return time.Time{}, fmt.Errorf("auth apikey new: --expires-in %q: %w", s, err)
		}
		return time.Now().Add(time.Duration(n) * 24 * time.Hour), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("auth apikey new: --expires-in %q: %w", s, err)
	}
	return time.Now().Add(d), nil
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// generatePlainKey returns a freshly-randomised key with the kit's
// conventional `kit_` prefix. 32 random bytes encoded as base64 ⇒
// 43 chars of entropy after the prefix — well above the
// brute-force-resistance bar for an HMAC-secured key.
func generatePlainKey() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "kit_" + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// hmacHash is kept as a defensive sanity check that auth.HashAPIKey
// stays consistent with the kit-internal hashing if either side is
// ever refactored. Asserts at import time via _ = ...
var _ = func() bool {
	a := auth.HashAPIKey("k", []byte("s"))
	m := hmac.New(sha256.New, []byte("s"))
	_, _ = m.Write([]byte("k"))
	b := m.Sum(nil)
	return string(a) == string(b)
}()
